package main

import (
	"context"
	"fmt"
	"sort"
	"spacebrowser/internal/platform"

	"github.com/shirou/gopsutil/v3/disk"
)

type App struct {
	ctx           context.Context
	showFreeSpace bool
}

func NewApp() *App                         { return &App{showFreeSpace: true} }
func (a *App) Startup(ctx context.Context) { a.ctx = ctx }

type TreeStore struct {
	root  *Node
	nodes []*Node // nodes[id] == *Node
}

func (a *App) SetShowFreeSpace(show bool) {
	a.showFreeSpace = show
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

	profile := defaultProfile()
	var files, dirs int64
	scanner := NewScanner(profile, 0)
	root, err := scanner.buildTree(path, 0, -1, &files, &dirs)
	if err != nil {
		return &TreeInfo{RootID: -1, FileCount: -1, DirCount: -1}, err
	}

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

			sort.Slice(root.Children, func(i, j int) bool {
				return root.Children[i].Size > root.Children[j].Size
			})
		}
	}

	store.Replace(root, scanner.Nodes())
	return &TreeInfo{RootID: root.ID, FileCount: int(files), DirCount: int(dirs)}, nil
}

func (a *App) Layout(nodeID, width, height int) ([]Rect, error) {
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
	if !a.showFreeSpace {
		filtered := make([]*Node, 0, len(n.Children))
		for _, c := range n.Children {
			if !c.IsFreeSpace { // skip only the free disk space nodes
				filtered = append(filtered, c)
			}
		}
		tmp.Children = filtered
	}

	return ComputeTreemapRects(&tmp, float64(width), float64(height)), nil
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
