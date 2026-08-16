package main

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"spacebrowser/internal/platform"
	"strings"
	"sync"
	"time"

	"github.com/shirou/gopsutil/v3/disk"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

type App struct {
	ctx                 context.Context
	initialScanPath     string
	logger              *SeverityLogger
	showFreeSpace       bool
	profile             Profile
	settingsPath        string
	defaultSettingsPath string
	settingsMu          sync.RWMutex

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
	return newAppWithLogger(NewSeverityLogger(defaultVerbosity, os.Stderr))
}

func newAppWithLogger(logger *SeverityLogger) *App {
	defaultPath, err := defaultSettingsPath()
	if err != nil {
		logger.Warningf("could not determine the default settings location: %v", err)
	}
	return newAppWithPathsAndLogger(configuredSettingsPath(defaultPath), defaultPath, logger)
}

func newApp(settingsPath string) *App {
	return newAppWithPaths(settingsPath, settingsPath)
}

func newAppWithPaths(settingsPath, defaultPath string) *App {
	return newAppWithPathsAndLogger(settingsPath, defaultPath, NewSeverityLogger(defaultVerbosity, os.Stderr))
}

func newAppWithPathsAndLogger(settingsPath, defaultPath string, logger *SeverityLogger) *App {
	profile := *defaultProfile()
	if settingsPath != "" {
		if savedProfile, err := loadSettings(settingsPath); err == nil {
			profile = savedProfile
		} else if !os.IsNotExist(err) {
			logger.Warningf("could not load settings from %s: %v; using defaults", settingsPath, err)
		}
	}
	return &App{
		showFreeSpace:       true,
		profile:             profile,
		settingsPath:        settingsPath,
		defaultSettingsPath: defaultPath,
		logger:              logger,
	}
}

func (a *App) Startup(ctx context.Context) {
	a.ctx = ctx
	a.logger.Debugf("application runtime initialized")
}

func (a *App) Shutdown(context.Context) {
	a.logger.Infof("SpaceBrowser stopped")
}

func (a *App) GetInitialScanPath() string {
	return a.initialScanPath
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

func (a *App) GetSettingsPath() string {
	a.settingsMu.RLock()
	defer a.settingsMu.RUnlock()
	return a.settingsPath
}

func (a *App) GetDefaultSettingsPath() string {
	a.settingsMu.RLock()
	defer a.settingsMu.RUnlock()
	return a.defaultSettingsPath
}

func (a *App) SetSettingsPath(path string) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return fmt.Errorf("settings path cannot be empty")
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("resolve settings path: %w", err)
	}
	path = filepath.Clean(absPath)
	if info, err := os.Stat(path); err == nil && info.IsDir() {
		return fmt.Errorf("settings path points to a directory")
	} else if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("inspect settings path: %w", err)
	}

	a.settingsMu.Lock()
	defer a.settingsMu.Unlock()
	if path == a.settingsPath {
		return nil
	}
	if a.defaultSettingsPath == "" {
		return fmt.Errorf("default settings location is unavailable")
	}
	if err := saveSettings(path, a.profile); err != nil {
		return fmt.Errorf("write settings at new location: %w", err)
	}
	if err := saveSettingsLocation(a.defaultSettingsPath, path); err != nil {
		return err
	}
	a.settingsPath = path
	return nil
}

func (a *App) PickSettingsPath() (string, error) {
	if a.ctx == nil {
		return "", fmt.Errorf("app not initialized")
	}
	currentPath := a.GetSettingsPath()
	path, err := runtime.SaveFileDialog(a.ctx, runtime.SaveDialogOptions{
		Title:                "Choose configuration file location",
		DefaultDirectory:     filepath.Dir(currentPath),
		DefaultFilename:      filepath.Base(currentPath),
		CanCreateDirectories: true,
		Filters:              []runtime.FileFilter{{DisplayName: "JSON files (*.json)", Pattern: "*.json"}},
	})
	if err != nil {
		return "", err
	}
	if path == "" {
		return "", nil
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve settings path: %w", err)
	}
	return filepath.Clean(absPath), nil
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
	profile.KeyBindings = normalizeKeyBindings(profile.KeyBindings)
	return profile, nil
}

func normalizeKeyBindings(bindings KeyBindings) KeyBindings {
	bindings.Back = strings.TrimSpace(bindings.Back)
	bindings.Forward = strings.TrimSpace(bindings.Forward)
	bindings.Parent = strings.TrimSpace(bindings.Parent)
	bindings.Root = strings.TrimSpace(bindings.Root)
	bindings.Open = strings.TrimSpace(bindings.Open)
	bindings.OpenWith = strings.TrimSpace(bindings.OpenWith)
	bindings.VisitSelected = strings.TrimSpace(bindings.VisitSelected)
	bindings.Delete = strings.TrimSpace(bindings.Delete)
	return bindings
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
		a.logger.Infof("cancelling active scan")
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
		a.logger.Tracef("scanning %s", path)
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
		a.logger.Errorf("cannot start scan: %v", err)
		return &TreeInfo{RootID: -1, FileCount: -1, DirCount: -1}, err
	}
	startedAt := time.Now()
	a.logger.Infof("scan started: %s", path)

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
	a.logger.Debugf("scan settings: skipHidden=%t minFileSize=%d followSymlinks=%t skipNetworkFS=%t", profile.SkipHidden, profile.MinFileSize, profile.FollowSymlinks, profile.SkipNetworkFS)
	var files, dirs int64
	scanner := NewScanner(&profile, 0)
	scanner.ReportAllErrors(a.logger.Enabled(verbosityDebug))
	a.attachScanner(generation, scanner)
	scanner.SetContext(ctx, func(path string) { a.updateScanPath(generation, path) })
	root, err := scanner.buildTree(path, 0, -1, &files, &dirs)
	if err != nil {
		a.logScanReport(scanner.Report())
		if ctx.Err() != nil {
			a.logger.Warningf("scan cancelled after %s: %s", time.Since(startedAt).Round(time.Millisecond), path)
			return &TreeInfo{RootID: -1, FileCount: -1, DirCount: -1}, fmt.Errorf("scan cancelled")
		}
		a.logger.Errorf("scan failed after %s: %v", time.Since(startedAt).Round(time.Millisecond), err)
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

	store.Replace(root, scanner.Nodes(), int(files), int(dirs))
	a.logScanReport(scanner.Report())
	a.logger.Infof("scan completed in %s: %s (%d files, %d folders, %d bytes)", time.Since(startedAt).Round(time.Millisecond), path, files, dirs, root.Size)
	return &TreeInfo{RootID: root.ID, FileCount: int(files), DirCount: int(dirs)}, nil
}

func (a *App) logScanReport(report ScanReportSnapshot) {
	skipped := report.TotalSkipped()
	errors := report.TotalErrors()
	a.logger.Infof("scan report: %d skipped paths, %d filesystem or metadata errors", skipped, errors)
	if skipped > 0 {
		a.logger.Infof("scan report skipped: %s", formatNonzeroScanCounts(report.Skipped[:], scanSkipLabels[:]))
	}
	if errors > 0 {
		a.logger.Infof("scan report errors: %s", formatNonzeroScanCounts(report.Errors[:], scanErrorLabels[:]))
		entryLabel := "example"
		if a.logger.Enabled(verbosityDebug) {
			entryLabel = "error"
		}
		for _, example := range report.Examples {
			a.logger.Infof("scan report %s [%s]: %s: %s", entryLabel, example.Reason, example.Path, example.Error)
		}
	}
}

func (a *App) Layout(nodeID, width, height int, scale float64) ([]Rect, error) {
	a.settingsMu.RLock()
	showFreeSpace := a.showFreeSpace
	a.settingsMu.RUnlock()
	return store.Layout(nodeID, width, height, scale, showFreeSpace)
}

func (a *App) DeleteNode(nodeID int) (DeleteResult, error) {
	profile := a.GetProfile()
	if !profile.AllowDelete {
		return DeleteResult{}, fmt.Errorf("delete commands are disabled; enable Allow delete command in Settings")
	}

	a.scanMu.RLock()
	defer a.scanMu.RUnlock()
	if a.scanActive {
		return DeleteResult{}, fmt.Errorf("items cannot be deleted while a scan is running")
	}

	return store.DeleteNode(nodeID, platform.Impl.MoveToTrash)
}

func (a *App) GetDefaultProfile() Profile {
	return *defaultProfile()
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

func (a *App) GetDefaultApplicationName(path string) (string, error) {
	if err := validateExistingPath(path); err != nil {
		return "", err
	}
	return platform.Impl.DefaultApplicationName(path)
}

func (a *App) OpenWith(path string) error {
	if err := validateExistingPath(path); err != nil {
		return err
	}
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return platform.Impl.OpenWith(ctx, path)
}

func (a *App) ShowProperties(path string) error {
	if err := validateExistingPath(path); err != nil {
		return err
	}
	return platform.Impl.ShowProperties(path)
}

func validateExistingPath(path string) error {
	if path == "" {
		return fmt.Errorf("missing path")
	}
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("path is unavailable: %w", err)
	}
	return nil
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
	const title = "Choose a folder to analyze"
	path, err := platform.Impl.PickFolder(a.ctx, title)
	if errors.Is(err, platform.ErrOperationCancelled) {
		return "", nil
	}
	if err == nil {
		if path == "" {
			return "", nil
		}
		return validateScanPath(path)
	}
	if !errors.Is(err, platform.ErrFolderPickerUnavailable) {
		return "", err
	}

	path, err = runtime.OpenDirectoryDialog(a.ctx, runtime.OpenDialogOptions{Title: title})
	if err != nil {
		return "", err
	}
	if path == "" {
		return "", nil
	}
	return validateScanPath(path)
}
