package main

import (
	"context"
	"fmt"
	"os"
	"sort"
	"spacebrowser/internal/platform"
	"strings"
	"sync"

	"github.com/shirou/gopsutil/v3/disk"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

type App struct {
	ctx           context.Context
	showFreeSpace bool
	profile       Profile
	settingsMu    sync.RWMutex
}

func NewApp() *App {
	profile := defaultProfile()
	return &App{showFreeSpace: true, profile: *profile}
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
	if profile.MinFileSize < 0 {
		return fmt.Errorf("minimum file size cannot be negative")
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

	a.settingsMu.Lock()
	a.profile = profile
	a.settingsMu.Unlock()
	return nil
}

func (s *TreeStore) Replace(root *Node, nodes []*Node) { s.root, s.nodes = root, nodes }

var store TreeStore

type TreeInfo struct {
	RootID    int `json:"rootId"`
	FileCount int `json:"fileCount"`
	DirCount  int `json:"dirCount"`
}

func (a *App) GetFullTree(path string) (*TreeInfo, error) {
	if path == "" {

		return &TreeInfo{RootID: -1, FileCount: -1, DirCount: -1}, fmt.Errorf("missing path")
	}
	path = platform.Impl.Canonicalize(path)
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return &TreeInfo{RootID: -1, FileCount: -1, DirCount: -1}, fmt.Errorf("this path does not exist")
	}
	if err != nil {
		return &TreeInfo{RootID: -1, FileCount: -1, DirCount: -1}, fmt.Errorf("cannot access this path: %w", err)
	}
	if !info.IsDir() {
		return &TreeInfo{RootID: -1, FileCount: -1, DirCount: -1}, fmt.Errorf("this path is not a folder")
	}

	profile := a.GetProfile()
	var files, dirs int64
	scanner := NewScanner(&profile, 0)
	root, err := scanner.buildTree(path, 0, -1, &files, &dirs)
	if err != nil {
		return &TreeInfo{RootID: -1, FileCount: -1, DirCount: -1}, err
	}

	scanner.addSmallFilesAggregate(root)

	if platform.Impl.IsMountRoot(path) {
		if fs, err := disk.Usage(path); err == nil {
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
