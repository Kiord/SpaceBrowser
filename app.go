package main

import (
	"context"
	"encoding/base64"
	"fmt"
	"math"
	"os"
	"sort"
	"spacebrowser/internal/platform"
	"strings"
	"sync"
	"time"

	"github.com/shirou/gopsutil/v3/disk"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

type App struct {
	ctx           context.Context
	showFreeSpace bool
	profile       Profile
	settingsPath  string
	settingsMu    sync.RWMutex

	scanMu         sync.RWMutex
	scanGeneration uint64
	scanActive     bool
	scanPath       string
	scanCancel     context.CancelFunc
	scanStartedAt  time.Time
	scanScanner    *Scanner
	scanTotalBytes int64
}

func NewApp() *App {
	settingsPath, _ := defaultSettingsPath()
	return newApp(settingsPath)
}

func newApp(settingsPath string) *App {
	profile := *defaultProfile()
	if settingsPath != "" {
		if savedProfile, err := loadSettings(settingsPath); err == nil {
			profile = savedProfile
		}
	}
	return &App{showFreeSpace: true, profile: profile, settingsPath: settingsPath}
}

func (a *App) Startup(ctx context.Context) { a.ctx = ctx }

type TreeStore struct {
	root  *Node
	nodes []*Node // nodes[id] == *Node
}

func (a *App) SetShowFreeSpace(show bool) {
	a.settingsMu.Lock()
	defer a.settingsMu.Unlock()
	a.showFreeSpace = show
}

func (a *App) GetProfile() Profile {
	a.settingsMu.RLock()
	defer a.settingsMu.RUnlock()
	profile := a.profile
	profile.ExcludedPaths = append([]string(nil), a.profile.ExcludedPaths...)
	return profile
}

func (a *App) SetProfile(profile Profile) error {
	profile, err := normalizeProfile(profile)
	if err != nil {
		return err
	}

	a.settingsMu.Lock()
	defer a.settingsMu.Unlock()
	if a.settingsPath != "" {
		if err := saveSettings(a.settingsPath, profile); err != nil {
			return fmt.Errorf("save settings: %w", err)
		}
	}
	a.profile = profile
	return nil
}

func normalizeProfile(profile Profile) (Profile, error) {
	if profile.MinFileSize < 0 {
		return Profile{}, fmt.Errorf("minimum file size cannot be negative")
	}

	profile.PlatformSystem = defaultProfile().PlatformSystem
	cleaned := make([]string, 0, len(profile.ExcludedPaths))
	seen := make(map[string]struct{}, len(profile.ExcludedPaths))
	for _, path := range profile.ExcludedPaths {
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}
		path = platform.Impl.Canonicalize(path)
		if _, exists := seen[path]; exists {
			continue
		}
		seen[path] = struct{}{}
		cleaned = append(cleaned, path)
	}
	profile.ExcludedPaths = cleaned

	appearance, err := normalizeAppearance(profile.Appearance)
	if err != nil {
		return Profile{}, err
	}
	profile.Appearance = appearance
	return profile, nil
}

func normalizeAppearance(appearance AppearanceSettings) (AppearanceSettings, error) {
	if appearance == (AppearanceSettings{}) {
		return defaultAppearanceSettings(), nil
	}
	validPalettes := map[string]bool{
		"default": true, "legacy": true, "single": true, "duotone": true,
		"tricolor": true, "playful": true, "monochrome": true,
		"earth": true, "ocean": true, "retro": true,
	}
	if !validPalettes[appearance.Palette] {
		return AppearanceSettings{}, fmt.Errorf("unknown colour palette %q", appearance.Palette)
	}
	if math.IsNaN(appearance.ZoomFactor) || math.IsInf(appearance.ZoomFactor, 0) || appearance.ZoomFactor < 0.5 || appearance.ZoomFactor > 5 {
		return AppearanceSettings{}, fmt.Errorf("zoom factor must be between 0.5 and 5")
	}
	if appearance.CornerRadius < 0 || appearance.CornerRadius > 10 {
		return AppearanceSettings{}, fmt.Errorf("corner radius must be between 0 and 10")
	}
	if math.IsNaN(appearance.ReliefStrength) || math.IsInf(appearance.ReliefStrength, 0) || appearance.ReliefStrength < 0 || appearance.ReliefStrength > 0.5 {
		return AppearanceSettings{}, fmt.Errorf("relief strength must be between 0 and 0.5")
	}
	return appearance, nil
}

func (s *TreeStore) Replace(root *Node, nodes []*Node) { s.root, s.nodes = root, nodes }

var store TreeStore

type TreeInfo struct {
	RootID    int `json:"rootId"`
	FileCount int `json:"fileCount"`
	DirCount  int `json:"dirCount"`
}

type ScanProgress struct {
	Active                bool    `json:"active"`
	Path                  string  `json:"path"`
	Processed             int64   `json:"processed"`
	Discovered            int64   `json:"discovered"`
	Determinate           bool    `json:"determinate"`
	Fraction              float64 `json:"fraction"`
	ElapsedMilliseconds   int64   `json:"elapsedMilliseconds"`
	RemainingMilliseconds int64   `json:"remainingMilliseconds"`
}

const scanProgressSlowdown = 10.0

func validateScanPath(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("missing path")
	}

	path = platform.Impl.Canonicalize(path)
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return "", fmt.Errorf("this path does not exist")
	}
	if err != nil {
		return "", fmt.Errorf("cannot access this path: %w", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("this path is not a folder")
	}
	return path, nil
}

func (a *App) ValidateScanPath(path string) (string, error) {
	return validateScanPath(path)
}

func (a *App) GetScanProgress() ScanProgress {
	a.scanMu.RLock()
	active := a.scanActive
	path := a.scanPath
	startedAt := a.scanStartedAt
	scanner := a.scanScanner
	totalBytes := a.scanTotalBytes
	a.scanMu.RUnlock()

	var processed, discovered int64
	var processedBytes int64
	if scanner != nil {
		processed, discovered = scanner.WorkProgress()
		processedBytes = scanner.BytesProcessed()
	}
	elapsed := time.Since(startedAt)
	if !active || startedAt.IsZero() {
		elapsed = 0
	}
	fraction := 0.0
	remainingMilliseconds := int64(-1)
	determinate := totalBytes > 0
	if determinate {
		rawFraction := float64(processedBytes) / float64(totalBytes)
		if rawFraction > 1 {
			rawFraction = 1
		}
		// Disk usage underestimates metadata-heavy work. This curve applies the
		// observed slowdown while preserving a true 100% endpoint.
		fraction = rawFraction / (scanProgressSlowdown - (scanProgressSlowdown-1)*rawFraction)
		if elapsed >= 6*time.Second && processedBytes > 0 && processedBytes < totalBytes {
			remaining := time.Duration(float64(elapsed) * float64(totalBytes-processedBytes) / float64(processedBytes))
			remainingMilliseconds = time.Duration(float64(remaining) * scanProgressSlowdown).Milliseconds()
		}
	}

	return ScanProgress{
		Active:                active,
		Path:                  path,
		Processed:             processed,
		Discovered:            discovered,
		Determinate:           determinate,
		Fraction:              fraction,
		ElapsedMilliseconds:   elapsed.Milliseconds(),
		RemainingMilliseconds: remainingMilliseconds,
	}
}

func (a *App) CancelScan() {
	a.scanMu.RLock()
	cancel := a.scanCancel
	a.scanMu.RUnlock()
	if cancel != nil {
		cancel()
	}
}

func (a *App) beginScan(path string, totalBytes int64) (context.Context, uint64) {
	base := a.ctx
	if base == nil {
		base = context.Background()
	}
	ctx, cancel := context.WithCancel(base)

	a.scanMu.Lock()
	if a.scanCancel != nil {
		a.scanCancel()
	}
	a.scanGeneration++
	generation := a.scanGeneration
	a.scanActive = true
	a.scanPath = path
	a.scanCancel = cancel
	a.scanStartedAt = time.Now()
	a.scanScanner = nil
	a.scanTotalBytes = totalBytes
	a.scanMu.Unlock()
	return ctx, generation
}

func (a *App) attachScanner(generation uint64, scanner *Scanner) {
	a.scanMu.Lock()
	if a.scanGeneration == generation && a.scanActive {
		a.scanScanner = scanner
	}
	a.scanMu.Unlock()
}

func (a *App) updateScanPath(generation uint64, path string) {
	a.scanMu.Lock()
	if a.scanGeneration == generation && a.scanActive {
		a.scanPath = path
	}
	a.scanMu.Unlock()
}

func (a *App) finishScan(generation uint64) {
	a.scanMu.Lock()
	if a.scanGeneration == generation {
		a.scanActive = false
		a.scanCancel = nil
		a.scanScanner = nil
		a.scanTotalBytes = 0
	}
	a.scanMu.Unlock()
}

func (a *App) GetFullTree(path string) (*TreeInfo, error) {
	path, err := validateScanPath(path)
	if err != nil {
		return &TreeInfo{RootID: -1, FileCount: -1, DirCount: -1}, err
	}

	var volumeUsage *disk.UsageStat
	var totalBytes int64
	if platform.Impl.IsMountRoot(path) {
		if fs, usageErr := disk.Usage(path); usageErr == nil {
			volumeUsage = fs
			totalBytes = int64(fs.Used)
		}
	}

	ctx, generation := a.beginScan(path, totalBytes)
	defer a.finishScan(generation)

	profile := a.GetProfile()
	var files, dirs int64
	scanner := NewScanner(&profile, 0)
	a.attachScanner(generation, scanner)
	scanner.SetContext(ctx, func(path string) { a.updateScanPath(generation, path) })
	root, err := scanner.buildTree(path, 0, -1, &files, &dirs)
	if err != nil {
		if ctx.Err() != nil {
			return &TreeInfo{RootID: -1, FileCount: -1, DirCount: -1}, fmt.Errorf("scan cancelled")
		}
		return &TreeInfo{RootID: -1, FileCount: -1, DirCount: -1}, err
	}

	if volumeUsage != nil {
		fs := volumeUsage
		free := &Node{
			ID:          -1,
			ParentID:    root.ID,
			Name:        "[Free Disk Space]",
			Size:        int64(fs.Free),
			DiskTotal:   int64(fs.Total),
			IsFolder:    false,
			IsFreeSpace: true,
			Depth:       1,
		}
		root.Children = append(root.Children, free)

		root.DiskTotal = int64(fs.Total)
		root.DiskFree = int64(fs.Free)

	}

	sort.Slice(root.Children, func(i, j int) bool {
		return root.Children[i].Size > root.Children[j].Size
	})

	store.Replace(root, scanner.Nodes())
	return &TreeInfo{RootID: root.ID, FileCount: int(files), DirCount: int(dirs)}, nil
}

func (a *App) Layout(nodeID, width, height int, scale float64) ([]Rect, error) {
	if nodeID < 0 || nodeID >= len(store.nodes) {
		return nil, fmt.Errorf("invalid node_id")
	}
	if width <= 0 || height <= 0 {
		return nil, fmt.Errorf("invalid width/height")
	}
	n := store.nodes[nodeID]
	if n == nil {
		return nil, fmt.Errorf("node not found")
	}

	tmp := *n
	a.settingsMu.RLock()
	showFreeSpace := a.showFreeSpace
	a.settingsMu.RUnlock()
	if !showFreeSpace {
		filtered := make([]*Node, 0, len(n.Children))
		for _, c := range n.Children {
			if !c.IsFreeSpace { // skip only the free disk space nodes
				filtered = append(filtered, c)
			}
		}
		tmp.Children = filtered
	}

	return ComputeTreemapRects(&tmp, float64(width), float64(height), scale), nil
}

func (a *App) OpenInFileBrowser(path string) error {
	if path == "" {
		return fmt.Errorf("missing path")
	}
	return platform.Impl.OpenInFileBrowser(path)
}

func (a *App) OpenPath(path string) error {
	if path == "" {
		return fmt.Errorf("missing path")
	}
	return platform.Impl.OpenPath(path)
}

func (a *App) GetAssociatedIcon(path string, isFolder bool) (string, error) {
	if path == "" {
		return "", fmt.Errorf("missing path")
	}
	icon, err := platform.Impl.AssociatedIcon(path, isFolder)
	if err != nil || len(icon) == 0 {
		return "", err
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(icon), nil
}

func (a *App) DefaultPath() string {
	return platform.Impl.DefaultStartPath()
}

func (a *App) PickFolder() (string, error) {
	if a.ctx == nil {
		return "", fmt.Errorf("app not initialized")
	}
	path, err := runtime.OpenDirectoryDialog(a.ctx, runtime.OpenDialogOptions{
		Title: "Choose a folder to analyze",
	})
	if err != nil {
		return "", err
	}
	if path == "" {
		return "", fmt.Errorf("cancelled")
	}
	return platform.Impl.Canonicalize(path), nil
}
