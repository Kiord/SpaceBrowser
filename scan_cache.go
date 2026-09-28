package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
)

const (
	maximumInMemoryScanCaches = 3
	maximumInMemoryCacheNodes = 2_500_000
	maximumUnobservedSubtrees = 4096
)

type scanCacheProfile struct {
	ExcludedPaths  []string `json:"excludedPaths"`
	SkipHidden     bool     `json:"skipHidden"`
	MinFileSize    int64    `json:"minFileSize"`
	FollowSymlinks bool     `json:"followSymlinks"`
	SkipNetworkFS  bool     `json:"skipNetworkFS"`
}

type scanCacheEntry struct {
	rootPath         string
	profileKey       string
	root             *Node
	nodes            []*Node
	nodeCount        int
	fileCount        int
	dirCount         int
	directories      map[string]*Node
	dirty            map[string]struct{}
	invalid          bool
	eventCount       uint64
	watcher          treeWatcher
	sharedAllocation bool
	report           ScanReportSnapshot
	lastUsed         uint64
}

type scanCacheDependency struct {
	entry      *scanCacheEntry
	eventCount uint64
}

// scanCacheObservation owns the watcher that spans enumeration and cache
// installation. Before activation it buffers events locally; afterward it
// forwards them to the installed cache entry.
type scanCacheObservation struct {
	manager        *scanCacheManager
	rootPath       string
	mu             sync.Mutex
	watcher        treeWatcher
	dirty          map[string]struct{}
	invalid        bool
	eventCount     uint64
	entry          *scanCacheEntry
	adopted        bool
	warnedCapacity bool
}

type scanReusePlan struct {
	directories  map[string]*Node
	dirty        []string
	reports      map[string]ScanReportSnapshot
	source       *scanCacheEntry
	eventCount   uint64
	dependencies []scanCacheDependency
}

type scanCacheManager struct {
	mu      sync.Mutex
	logger  *SeverityLogger
	entries map[string]*scanCacheEntry
	clock   uint64
}

func newScanCacheManager(logger *SeverityLogger) *scanCacheManager {
	return &scanCacheManager{logger: logger, entries: make(map[string]*scanCacheEntry)}
}

func scanMemoryCacheKey(rootPath, profileKey string) string {
	return canonicalCachePath(rootPath) + "\x00" + profileKey
}

func (manager *scanCacheManager) touchLocked(entry *scanCacheEntry) {
	manager.clock++
	entry.lastUsed = manager.clock
}

func (manager *scanCacheManager) containsLocked(entry *scanCacheEntry) bool {
	if entry == nil {
		return false
	}
	return manager.entries[scanMemoryCacheKey(entry.rootPath, entry.profileKey)] == entry
}

func scanProfileCacheKey(profile Profile) (scanCacheProfile, string) {
	key := scanCacheProfile{
		ExcludedPaths:  append([]string(nil), profile.ExcludedPaths...),
		SkipHidden:     profile.SkipHidden,
		MinFileSize:    profile.MinFileSize,
		FollowSymlinks: profile.FollowSymlinks,
		SkipNetworkFS:  profile.SkipNetworkFS,
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

func (manager *scanCacheManager) Prepare(rootPath string, profile Profile) scanReusePlan {
	if manager == nil {
		return scanReusePlan{}
	}
	_, profileKey := scanProfileCacheKey(profile)
	manager.mu.Lock()
	defer manager.mu.Unlock()
	exact := manager.entries[scanMemoryCacheKey(rootPath, profileKey)]
	if exact != nil {
		manager.touchLocked(exact)
		if exact.invalid {
			return scanReusePlan{}
		}
		dirty := cacheDirtyPaths(exact)
		plan := scanReusePlan{source: exact, eventCount: exact.eventCount, dependencies: []scanCacheDependency{{exact, exact.eventCount}}}
		if len(dirty) > 0 && !cacheEntryIncrementallySafe(exact, profile) {
			return plan
		}
		plan.directories = exact.directories
		plan.dirty = dirty
		return plan
	}

	// Prefer one cached ancestor: it already contains the complete requested
	// subtree and avoids constructing another million-entry lookup map.
	var ancestor *scanCacheEntry
	for _, entry := range manager.entries {
		if entry.profileKey != profileKey || entry.invalid || !cacheEntryIncrementallySafe(entry, profile) ||
			!cachePathWithin(rootPath, entry.rootPath) {
			continue
		}
		if ancestor == nil || len(canonicalCachePath(entry.rootPath)) > len(canonicalCachePath(ancestor.rootPath)) {
			ancestor = entry
		}
	}
	if ancestor != nil {
		manager.touchLocked(ancestor)
		return scanReusePlan{
			directories:  ancestor.directories,
			dirty:        cacheDirtyPaths(ancestor),
			dependencies: []scanCacheDependency{{ancestor, ancestor.eventCount}},
		}
	}

	// Cached child roots can accelerate a later broader scan. Multiple disjoint
	// children may contribute, so combine their directory indexes.
	plan := scanReusePlan{}
	var candidates []*scanCacheEntry
	for _, entry := range manager.entries {
		if entry.profileKey != profileKey || entry.invalid || !cacheEntryCrossRootSafe(entry, profile) ||
			!cachePathWithin(entry.rootPath, rootPath) {
			continue
		}
		if len(entry.dirty) > 0 && (entry.report.TotalSkipped() > 0 || entry.report.TotalErrors() > 0) {
			continue
		}
		candidates = append(candidates, entry)
	}
	sort.Slice(candidates, func(i, j int) bool {
		return len(canonicalCachePath(candidates[i].rootPath)) < len(canonicalCachePath(candidates[j].rootPath))
	})
	var selectedRoots []string
	for _, entry := range candidates {
		nested := false
		for _, selectedRoot := range selectedRoots {
			if cachePathWithin(entry.rootPath, selectedRoot) {
				nested = true
				break
			}
		}
		if nested {
			continue
		}
		selectedRoots = append(selectedRoots, entry.rootPath)
		manager.touchLocked(entry)
		if plan.directories == nil {
			plan.directories = make(map[string]*Node)
		}
		for path, node := range entry.directories {
			plan.directories[path] = node
		}
		plan.dirty = append(plan.dirty, cacheDirtyPaths(entry)...)
		if entry.report.TotalSkipped() > 0 || entry.report.TotalErrors() > 0 {
			if plan.reports == nil {
				plan.reports = make(map[string]ScanReportSnapshot)
			}
			plan.reports[canonicalCachePath(entry.rootPath)] = entry.report
		}
		plan.dependencies = append(plan.dependencies, scanCacheDependency{entry, entry.eventCount})
	}
	return plan
}

func cacheDirtyPaths(entry *scanCacheEntry) []string {
	dirty := make([]string, 0, len(entry.dirty))
	for path := range entry.dirty {
		dirty = append(dirty, path)
	}
	return dirty
}

func cacheEntryIncrementallySafe(entry *scanCacheEntry, profile Profile) bool {
	return !entry.sharedAllocation && !profile.FollowSymlinks && entry.report.TotalSkipped() == 0 && entry.report.TotalErrors() == 0
}

func cacheEntryCrossRootSafe(entry *scanCacheEntry, profile Profile) bool {
	return !entry.sharedAllocation && !profile.FollowSymlinks
}

func (manager *scanCacheManager) BeginObservation(rootPath string) *scanCacheObservation {
	observation := &scanCacheObservation{
		manager: manager, rootPath: rootPath, dirty: make(map[string]struct{}),
	}
	watcher, err := startTreeWatcher(rootPath, []string{rootPath}, observation.markChanged, observation.markSubtreeChanged, observation.markFailed)
	observation.mu.Lock()
	observation.watcher = watcher
	if err != nil {
		observation.invalid = true
	}
	observation.mu.Unlock()
	if err != nil && manager != nil && manager.logger != nil {
		manager.logger.Warningf("scan cache watcher disabled for %s: %v", rootPath, err)
	}
	return observation
}

func (observation *scanCacheObservation) WatchDirectory(path string) {
	if observation == nil {
		return
	}
	observation.mu.Lock()
	watcher := observation.watcher
	invalid := observation.invalid
	observation.mu.Unlock()
	if watcher == nil || invalid {
		return
	}
	if err := watcher.AddDirectory(path); err != nil {
		if errors.Is(err, errTreeWatchCapacity) {
			observation.markUnobserved(path, err)
			return
		}
		observation.markFailed(err)
	}
}

func (observation *scanCacheObservation) markUnobserved(path string, err error) {
	clean := canonicalCachePath(path)
	observation.mu.Lock()
	entry := observation.entry
	changed := false
	if entry == nil {
		changed = addDirtySubtree(observation.dirty, observation.rootPath, clean, maximumUnobservedSubtrees)
		if changed {
			observation.eventCount++
		}
	} else {
		observation.mu.Unlock()
		changed = observation.manager.markSubtreeDirty(entry, clean)
		observation.logWatchCapacity(err, changed)
		return
	}
	observation.mu.Unlock()
	observation.logWatchCapacity(err, changed)
}

func (observation *scanCacheObservation) logWatchCapacity(err error, changed bool) {
	if !changed || observation.manager == nil || observation.manager.logger == nil {
		return
	}
	observation.mu.Lock()
	if observation.warnedCapacity {
		observation.mu.Unlock()
		return
	}
	observation.warnedCapacity = true
	observation.mu.Unlock()
	observation.manager.logger.Warningf("scan cache watch capacity reached for %s; unobserved subtrees will be rescanned: %v", observation.rootPath, err)
}

func addDirtySubtree(paths map[string]struct{}, rootPath, path string, limit int) bool {
	cleanRoot := canonicalCachePath(rootPath)
	clean := canonicalCachePath(path)
	for existing := range paths {
		if cachePathWithin(clean, existing) {
			return false
		}
	}
	for existing := range paths {
		if cachePathWithin(existing, clean) {
			delete(paths, existing)
		}
	}
	if len(paths) >= limit {
		if len(paths) == 1 {
			if _, alreadyRoot := paths[cleanRoot]; alreadyRoot {
				return false
			}
		}
		clear(paths)
		paths[cleanRoot] = struct{}{}
		return true
	}
	paths[clean] = struct{}{}
	return true
}

func (observation *scanCacheObservation) markChanged(path string) {
	clean := canonicalCachePath(path)
	parent := canonicalCachePath(filepath.Dir(clean))
	observation.mu.Lock()
	entry := observation.entry
	if entry == nil {
		observation.dirty[clean] = struct{}{}
		observation.dirty[parent] = struct{}{}
		observation.eventCount++
		observation.mu.Unlock()
		return
	}
	observation.mu.Unlock()
	observation.manager.markChanged(entry, clean)
}

func (observation *scanCacheObservation) markSubtreeChanged(path string) {
	clean := canonicalCachePath(path)
	observation.mu.Lock()
	entry := observation.entry
	if entry == nil {
		if addDirtySubtree(observation.dirty, observation.rootPath, clean, maximumUnobservedSubtrees) {
			observation.eventCount++
		}
		observation.mu.Unlock()
		return
	}
	observation.mu.Unlock()
	observation.manager.markSubtreeDirty(entry, clean)
}

func (observation *scanCacheObservation) markFailed(err error) {
	observation.mu.Lock()
	entry := observation.entry
	if entry == nil {
		observation.invalid = true
		observation.eventCount++
		observation.mu.Unlock()
		if observation.manager != nil && observation.manager.logger != nil {
			observation.manager.logger.Warningf("scan cache watcher invalidated for %s: %v", observation.rootPath, err)
		}
	} else {
		observation.mu.Unlock()
		observation.manager.markWatcherFailed(entry, err)
	}
}

func (observation *scanCacheObservation) activate(entry *scanCacheEntry) (treeWatcher, map[string]struct{}, bool, uint64) {
	observation.mu.Lock()
	defer observation.mu.Unlock()
	observation.entry = entry
	observation.adopted = true
	dirty := make(map[string]struct{}, len(observation.dirty))
	for path := range observation.dirty {
		dirty[path] = struct{}{}
	}
	return observation.watcher, dirty, observation.invalid, observation.eventCount
}

func (observation *scanCacheObservation) Close() {
	if observation == nil {
		return
	}
	observation.mu.Lock()
	if observation.adopted {
		observation.mu.Unlock()
		return
	}
	observation.adopted = true
	watcher := observation.watcher
	observation.mu.Unlock()
	if watcher != nil {
		_ = watcher.Close()
	}
}

func (manager *scanCacheManager) StillClean(source *scanCacheEntry, eventCount uint64) bool {
	if manager == nil || source == nil {
		return false
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	return manager.containsLocked(source) && !source.invalid && source.eventCount == eventCount && len(source.dirty) == 0
}

func (manager *scanCacheManager) Install(rootPath string, profile Profile, root *Node, nodes []*Node, files, dirs int, report ScanReportSnapshot, plan scanReusePlan, observation *scanCacheObservation) {
	if manager == nil || root == nil {
		return
	}
	if observation == nil {
		observation = manager.BeginObservation(rootPath)
		// A watcher created only after enumeration cannot certify the result.
		// Keep the fallback safe for any caller that omitted BeginObservation.
		observation.markChanged(rootPath)
	}
	for _, directory := range watchedDirectories(rootPath, nodes) {
		observation.WatchDirectory(directory)
	}
	_, profileKey := scanProfileCacheKey(profile)
	entry := &scanCacheEntry{
		rootPath:         rootPath,
		profileKey:       profileKey,
		root:             root,
		nodes:            nodes,
		nodeCount:        len(nodes),
		fileCount:        files,
		dirCount:         dirs,
		directories:      indexCachedDirectories(root),
		dirty:            make(map[string]struct{}),
		sharedAllocation: subtreeHasSharedAllocation(root),
		report:           report,
		invalid:          true,
	}

	manager.mu.Lock()
	if manager.entries == nil {
		manager.entries = make(map[string]*scanCacheEntry)
	}
	key := scanMemoryCacheKey(rootPath, profileKey)
	old := manager.entries[key]
	manager.entries[key] = entry
	manager.touchLocked(entry)
	if manager.dependenciesChangedLocked(plan.dependencies) {
		entry.dirty[canonicalCachePath(rootPath)] = struct{}{}
		entry.eventCount++
	}
	var retired []*scanCacheEntry
	if old != nil && old != entry {
		retired = append(retired, old)
	}
	// A broader tree makes same-profile child caches redundant.
	for otherKey, other := range manager.entries {
		if other == entry || other.profileKey != profileKey || !cachePathWithin(other.rootPath, rootPath) {
			continue
		}
		delete(manager.entries, otherKey)
		retired = append(retired, other)
	}
	for manager.memoryBudgetExceededLocked() {
		var oldestKey string
		var oldest *scanCacheEntry
		for candidateKey, candidate := range manager.entries {
			if candidate == entry {
				continue
			}
			if oldest == nil || candidate.lastUsed < oldest.lastUsed {
				oldestKey, oldest = candidateKey, candidate
			}
		}
		if oldest == nil {
			break
		}
		delete(manager.entries, oldestKey)
		retired = append(retired, oldest)
	}
	manager.mu.Unlock()

	watcher, observedDirty, watcherInvalid, observedEvents := observation.activate(entry)
	manager.mu.Lock()
	retained := manager.containsLocked(entry)
	if retained {
		entry.watcher = watcher
		entry.invalid = watcherInvalid
		for path := range observedDirty {
			entry.dirty[path] = struct{}{}
		}
		entry.eventCount += observedEvents
	}
	manager.mu.Unlock()
	for _, retiredEntry := range retired {
		if retiredEntry.watcher != nil {
			_ = retiredEntry.watcher.Close()
		}
	}
	if !retained && watcher != nil {
		_ = watcher.Close()
	}
}

func (manager *scanCacheManager) memoryBudgetExceededLocked() bool {
	return scanCacheEntriesExceedBudget(manager.entries, maximumInMemoryScanCaches, maximumInMemoryCacheNodes)
}

func scanCacheEntriesExceedBudget(entries map[string]*scanCacheEntry, maxEntries, maxNodes int) bool {
	if len(entries) > maxEntries {
		return true
	}
	totalNodes := 0
	for _, entry := range entries {
		totalNodes += entry.nodeCount
	}
	return totalNodes > maxNodes && len(entries) > 1
}

func (manager *scanCacheManager) dependenciesChangedLocked(dependencies []scanCacheDependency) bool {
	for _, dependency := range dependencies {
		if !manager.containsLocked(dependency.entry) || dependency.entry.invalid || dependency.entry.eventCount != dependency.eventCount {
			return true
		}
	}
	return false
}

func watchedDirectories(rootPath string, nodes []*Node) []string {
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
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
	if !manager.containsLocked(entry) {
		return
	}
	clean := canonicalCachePath(path)
	entry.dirty[clean] = struct{}{}
	entry.dirty[canonicalCachePath(filepath.Dir(clean))] = struct{}{}
	entry.eventCount++
}

func (manager *scanCacheManager) markSubtreeDirty(entry *scanCacheEntry, path string) bool {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if !manager.containsLocked(entry) {
		return false
	}
	if !addDirtySubtree(entry.dirty, entry.rootPath, path, maximumUnobservedSubtrees) {
		return false
	}
	entry.eventCount++
	return true
}

func (manager *scanCacheManager) markWatcherFailed(entry *scanCacheEntry, err error) {
	manager.mu.Lock()
	if manager.containsLocked(entry) {
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
	clean := canonicalCachePath(path)
	for _, entry := range manager.entries {
		if cachePathWithin(clean, entry.rootPath) || cachePathWithin(entry.rootPath, clean) {
			entry.dirty[clean] = struct{}{}
			entry.eventCount++
		}
	}
}

func (manager *scanCacheManager) Clear() {
	if manager == nil {
		return
	}
	manager.mu.Lock()
	entries := make([]*scanCacheEntry, 0, len(manager.entries))
	for _, entry := range manager.entries {
		entries = append(entries, entry)
	}
	manager.entries = make(map[string]*scanCacheEntry)
	manager.mu.Unlock()
	for _, entry := range entries {
		if entry.watcher != nil {
			_ = entry.watcher.Close()
		}
	}
}

func (manager *scanCacheManager) Close() {
	manager.Clear()
}
