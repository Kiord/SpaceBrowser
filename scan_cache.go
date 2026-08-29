package main

import (
	"compress/gzip"
	"crypto/sha256"
	"encoding/gob"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	scanSnapshotVersion       = 1
	scanAccountingVersion     = 1
	maximumPersistedSnapshots = 5
	maximumSnapshotNodes      = 10_000_000
)

type scanCacheProfile struct {
	AccountingVersion int      `json:"accountingVersion"`
	ExcludedPaths     []string `json:"excludedPaths"`
	SkipHidden        bool     `json:"skipHidden"`
	MinFileSize       int64    `json:"minFileSize"`
	FollowSymlinks    bool     `json:"followSymlinks"`
	SkipNetworkFS     bool     `json:"skipNetworkFS"`
}

type persistedScanSnapshot struct {
	Version    int
	RootPath   string
	ProfileKey string
	SavedAt    time.Time
	Root       *Node
	FileCount  int
	DirCount   int
	Report     ScanReportSnapshot
}

type scanCacheEntry struct {
	rootPath         string
	profileKey       string
	root             *Node
	fileCount        int
	dirCount         int
	directories      map[string]*Node
	dirty            map[string]struct{}
	invalid          bool
	eventCount       uint64
	watcher          treeWatcher
	sharedAllocation bool
	report           ScanReportSnapshot
}

type scanReusePlan struct {
	directories map[string]*Node
	dirty       []string
	source      *scanCacheEntry
	eventCount  uint64
}

type loadedScanSnapshot struct {
	root    *Node
	nodes   []*Node
	files   int
	dirs    int
	savedAt time.Time
}

type scanCacheManager struct {
	mu        sync.Mutex
	directory string
	logger    *SeverityLogger
	entry     *scanCacheEntry
}

func newScanCacheManager(defaultSettingsPath string, logger *SeverityLogger) *scanCacheManager {
	directory := ""
	if defaultSettingsPath != "" {
		directory = filepath.Join(filepath.Dir(defaultSettingsPath), "cache", "scans")
	}
	return &scanCacheManager{directory: directory, logger: logger}
}

func scanProfileCacheKey(profile Profile) (scanCacheProfile, string) {
	key := scanCacheProfile{
		AccountingVersion: scanAccountingVersion,
		ExcludedPaths:     append([]string(nil), profile.ExcludedPaths...),
		SkipHidden:        profile.SkipHidden,
		MinFileSize:       profile.MinFileSize,
		FollowSymlinks:    profile.FollowSymlinks,
		SkipNetworkFS:     profile.SkipNetworkFS,
	}
	data, _ := json.Marshal(key)
	sum := sha256.Sum256(data)
	return key, hex.EncodeToString(sum[:])
}

func canonicalCachePath(path string) string {
	clean := filepath.Clean(path)
	if runtime.GOOS == "windows" {
		return strings.ToLower(clean)
	}
	return clean
}

func scanSnapshotFilename(rootPath, profileKey string) string {
	sum := sha256.Sum256([]byte(canonicalCachePath(rootPath) + "\x00" + profileKey))
	return hex.EncodeToString(sum[:]) + ".gob.gz"
}

func (manager *scanCacheManager) snapshotPath(rootPath, profileKey string) string {
	if manager == nil || manager.directory == "" {
		return ""
	}
	return filepath.Join(manager.directory, scanSnapshotFilename(rootPath, profileKey))
}

func (manager *scanCacheManager) Current(rootPath string, profile Profile) (files, dirs int, ok bool) {
	if manager == nil {
		return 0, 0, false
	}
	_, profileKey := scanProfileCacheKey(profile)
	manager.mu.Lock()
	defer manager.mu.Unlock()
	entry := manager.entry
	if entry == nil || entry.profileKey != profileKey || canonicalCachePath(entry.rootPath) != canonicalCachePath(rootPath) {
		return 0, 0, false
	}
	return entry.fileCount, entry.dirCount, true
}

func (manager *scanCacheManager) Prepare(rootPath string, profile Profile) scanReusePlan {
	if manager == nil {
		return scanReusePlan{}
	}
	_, profileKey := scanProfileCacheKey(profile)
	manager.mu.Lock()
	defer manager.mu.Unlock()
	entry := manager.entry
	if entry == nil || entry.invalid || entry.profileKey != profileKey || canonicalCachePath(entry.rootPath) != canonicalCachePath(rootPath) {
		return scanReusePlan{}
	}
	dirty := make([]string, 0, len(entry.dirty))
	for path := range entry.dirty {
		dirty = append(dirty, path)
	}
	if len(dirty) > 0 && (entry.sharedAllocation || profile.FollowSymlinks || entry.report.TotalSkipped() > 0 || entry.report.TotalErrors() > 0) {
		return scanReusePlan{source: entry, eventCount: entry.eventCount}
	}
	return scanReusePlan{
		directories: entry.directories,
		dirty:       dirty,
		source:      entry,
		eventCount:  entry.eventCount,
	}
}

func (manager *scanCacheManager) StillClean(source *scanCacheEntry, eventCount uint64) bool {
	if manager == nil || source == nil {
		return false
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	return manager.entry == source && !source.invalid && source.eventCount == eventCount && len(source.dirty) == 0
}

func (manager *scanCacheManager) Install(rootPath string, profile Profile, root *Node, nodes []*Node, files, dirs int, report ScanReportSnapshot, source *scanCacheEntry, sourceEventCount uint64) {
	if manager == nil || root == nil {
		return
	}
	_, profileKey := scanProfileCacheKey(profile)
	entry := &scanCacheEntry{
		rootPath:         rootPath,
		profileKey:       profileKey,
		root:             root,
		fileCount:        files,
		dirCount:         dirs,
		directories:      indexCachedDirectories(root),
		dirty:            make(map[string]struct{}),
		sharedAllocation: subtreeHasSharedAllocation(root),
		report:           report,
	}

	manager.mu.Lock()
	old := manager.entry
	manager.entry = entry
	if source != nil && source.eventCount != sourceEventCount {
		entry.dirty[canonicalCachePath(rootPath)] = struct{}{}
		entry.eventCount++
	}
	manager.mu.Unlock()
	if old != nil && old.watcher != nil {
		_ = old.watcher.Close()
	}

	directories := watchedDirectories(rootPath, nodes)
	watcher, err := startTreeWatcher(rootPath, directories,
		func(path string) { manager.markChanged(entry, path) },
		func(err error) { manager.markWatcherFailed(entry, err) },
	)
	manager.mu.Lock()
	if manager.entry == entry {
		if err != nil {
			entry.invalid = true
		} else {
			entry.watcher = watcher
		}
	}
	manager.mu.Unlock()
	if err != nil && manager.logger != nil {
		manager.logger.Warningf("scan cache watcher disabled for %s: %v", rootPath, err)
	}
}

func watchedDirectories(rootPath string, nodes []*Node) []string {
	if runtime.GOOS == "windows" {
		return []string{rootPath}
	}
	directories := make([]string, 0)
	for _, node := range nodes {
		if node != nil && node.IsFolder && node.FullPath != "" {
			directories = append(directories, node.FullPath)
		}
	}
	return directories
}

func indexCachedDirectories(root *Node) map[string]*Node {
	result := make(map[string]*Node)
	var visit func(*Node)
	visit = func(node *Node) {
		if node == nil {
			return
		}
		if node.IsFolder && node.FullPath != "" {
			result[canonicalCachePath(node.FullPath)] = node
		}
		for _, child := range node.Children {
			visit(child)
		}
	}
	visit(root)
	return result
}

func (manager *scanCacheManager) markChanged(entry *scanCacheEntry, path string) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if manager.entry != entry {
		return
	}
	clean := canonicalCachePath(path)
	entry.dirty[clean] = struct{}{}
	entry.dirty[canonicalCachePath(filepath.Dir(clean))] = struct{}{}
	entry.eventCount++
}

func (manager *scanCacheManager) markWatcherFailed(entry *scanCacheEntry, err error) {
	manager.mu.Lock()
	if manager.entry == entry {
		entry.invalid = true
		entry.eventCount++
	}
	manager.mu.Unlock()
	if manager.logger != nil {
		manager.logger.Warningf("scan cache watcher invalidated: %v", err)
	}
}

func (manager *scanCacheManager) InvalidatePath(path string) {
	if manager == nil {
		return
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if manager.entry == nil {
		return
	}
	manager.entry.dirty[canonicalCachePath(path)] = struct{}{}
	manager.entry.eventCount++
}

func (manager *scanCacheManager) Close() {
	if manager == nil {
		return
	}
	manager.mu.Lock()
	entry := manager.entry
	manager.entry = nil
	manager.mu.Unlock()
	if entry != nil && entry.watcher != nil {
		_ = entry.watcher.Close()
	}
}

func (manager *scanCacheManager) SaveSnapshot(rootPath string, profile Profile, root *Node, files, dirs int, report ScanReportSnapshot) error {
	if manager == nil || manager.directory == "" || root == nil {
		return nil
	}
	_, profileKey := scanProfileCacheKey(profile)
	path := manager.snapshotPath(rootPath, profileKey)
	if err := os.MkdirAll(manager.directory, 0o700); err != nil {
		return fmt.Errorf("create scan cache directory: %w", err)
	}
	temporary, err := os.CreateTemp(manager.directory, ".scan-*.tmp")
	if err != nil {
		return fmt.Errorf("create scan snapshot: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return fmt.Errorf("secure scan snapshot: %w", err)
	}
	compressor, err := gzip.NewWriterLevel(temporary, gzip.BestSpeed)
	if err != nil {
		temporary.Close()
		return fmt.Errorf("create scan snapshot compressor: %w", err)
	}
	snapshot := persistedScanSnapshot{
		Version: scanSnapshotVersion, RootPath: rootPath, ProfileKey: profileKey,
		SavedAt: time.Now(), Root: root, FileCount: files, DirCount: dirs, Report: report,
	}
	if err := gob.NewEncoder(compressor).Encode(snapshot); err != nil {
		compressor.Close()
		temporary.Close()
		return fmt.Errorf("encode scan snapshot: %w", err)
	}
	if err := compressor.Close(); err != nil {
		temporary.Close()
		return fmt.Errorf("finish scan snapshot: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return fmt.Errorf("flush scan snapshot: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close scan snapshot: %w", err)
	}
	if err := replaceFile(temporaryPath, path); err != nil {
		return fmt.Errorf("replace scan snapshot: %w", err)
	}
	manager.pruneSnapshots(path)
	return nil
}

func replaceFile(source, target string) error {
	return os.Rename(source, target)
}

func (manager *scanCacheManager) LoadSnapshot(rootPath string, profile Profile) (loadedScanSnapshot, error) {
	if manager == nil || manager.directory == "" {
		return loadedScanSnapshot{}, os.ErrNotExist
	}
	_, profileKey := scanProfileCacheKey(profile)

	manager.mu.Lock()
	entry := manager.entry
	if entry != nil && entry.profileKey == profileKey && canonicalCachePath(entry.rootPath) == canonicalCachePath(rootPath) {
		root, nodes := cloneAndReindexTree(entry.root)
		loaded := loadedScanSnapshot{root: root, nodes: nodes, files: entry.fileCount, dirs: entry.dirCount, savedAt: time.Now()}
		manager.mu.Unlock()
		return loaded, nil
	}
	manager.mu.Unlock()

	path := manager.snapshotPath(rootPath, profileKey)
	file, err := os.Open(path)
	if err != nil {
		return loadedScanSnapshot{}, err
	}
	defer file.Close()
	decompressor, err := gzip.NewReader(file)
	if err != nil {
		return loadedScanSnapshot{}, fmt.Errorf("open scan snapshot: %w", err)
	}
	defer decompressor.Close()
	var snapshot persistedScanSnapshot
	if err := gob.NewDecoder(io.LimitReader(decompressor, 4<<30)).Decode(&snapshot); err != nil {
		return loadedScanSnapshot{}, fmt.Errorf("decode scan snapshot: %w", err)
	}
	if snapshot.Version != scanSnapshotVersion || snapshot.ProfileKey != profileKey || canonicalCachePath(snapshot.RootPath) != canonicalCachePath(rootPath) || snapshot.Root == nil {
		return loadedScanSnapshot{}, errors.New("scan snapshot is incompatible")
	}
	if err := validateSnapshotTree(rootPath, snapshot.Root); err != nil {
		return loadedScanSnapshot{}, fmt.Errorf("validate scan snapshot: %w", err)
	}
	root, nodes := cloneAndReindexTree(snapshot.Root)
	return loadedScanSnapshot{root: root, nodes: nodes, files: snapshot.FileCount, dirs: snapshot.DirCount, savedAt: snapshot.SavedAt}, nil
}

func validateSnapshotTree(rootPath string, root *Node) error {
	if root == nil || !root.IsFolder || canonicalCachePath(root.FullPath) != canonicalCachePath(rootPath) {
		return errors.New("snapshot root does not match the requested folder")
	}
	visited := make(map[*Node]struct{})
	stack := []*Node{root}
	for len(stack) > 0 {
		node := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if _, exists := visited[node]; exists {
			return errors.New("snapshot tree contains a cycle or duplicate node reference")
		}
		visited[node] = struct{}{}
		if len(visited) > maximumSnapshotNodes {
			return errors.New("snapshot tree is unreasonably large")
		}
		if node.Size < 0 || node.EntryFiles < 0 || node.EntryDirs < 0 {
			return errors.New("snapshot contains negative accounting values")
		}
		virtual := node.IsFreeSpace || node.IsSmallFiles
		if !virtual && (node.FullPath == "" || !cachePathWithin(node.FullPath, rootPath)) {
			return errors.New("snapshot contains a path outside its scan root")
		}
		if !node.IsFolder && !virtual && len(node.Children) > 0 {
			return errors.New("snapshot file contains child nodes")
		}
		for _, child := range node.Children {
			if child == nil {
				return errors.New("snapshot contains a nil child")
			}
			stack = append(stack, child)
		}
	}
	return nil
}

func cloneAndReindexTree(root *Node) (*Node, []*Node) {
	nodes := make([]*Node, 0)
	var clone func(*Node, int, int) *Node
	clone = func(source *Node, parentID, depth int) *Node {
		if source == nil {
			return nil
		}
		node := *source
		node.ParentID = parentID
		node.Depth = depth
		node.Children = make([]*Node, 0, len(source.Children))
		if source.IsFreeSpace || source.IsSmallFiles {
			node.ID = -1
		} else {
			node.ID = len(nodes)
			nodes = append(nodes, &node)
		}
		for _, child := range source.Children {
			if cloned := clone(child, node.ID, depth+1); cloned != nil {
				node.Children = append(node.Children, cloned)
			}
		}
		return &node
	}
	return clone(root, -1, 0), nodes
}

func (manager *scanCacheManager) pruneSnapshots(current string) {
	entries, err := os.ReadDir(manager.directory)
	if err != nil {
		return
	}
	type candidate struct {
		path string
		time time.Time
	}
	var candidates []candidate
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".gob.gz") {
			continue
		}
		info, infoErr := entry.Info()
		if infoErr == nil {
			candidates = append(candidates, candidate{path: filepath.Join(manager.directory, entry.Name()), time: info.ModTime()})
		}
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].time.After(candidates[j].time) })
	if len(candidates) <= maximumPersistedSnapshots {
		return
	}
	for _, candidate := range candidates[maximumPersistedSnapshots:] {
		if candidate.path != current {
			_ = os.Remove(candidate.path)
		}
	}
}
