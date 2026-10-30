package app

import (
	"fmt"
	"spacebrowser/internal/platform"
)

type TooltipDetails struct {
	RecursiveFiles   int   `json:"recursiveFiles"`
	RecursiveFolders int   `json:"recursiveFolders"`
	DirectFiles      int   `json:"directFiles"`
	DirectFolders    int   `json:"directFolders"`
	CreationTime     int64 `json:"creationTime"`
	Incomplete       bool  `json:"incomplete"`
}

// Counts describe the selected scan, including aggregated and deduplicated files.
// Creation time is queried only when requested and can be unavailable on old or
// disconnected snapshots and filesystems that do not expose birth time.
func (a *App) GetTooltipDetails(nodeID int, path string, includeCreation bool) (TooltipDetails, error) {
	details, err := a.store.tooltipDetails(nodeID, path)
	if err != nil {
		return details, err
	}
	if includeCreation {
		details.CreationTime = platform.FileCreationTime(path)
	}
	return details, nil
}

func (s *TreeStore) tooltipDetails(nodeID int, path string) (TooltipDetails, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if nodeID < 0 || nodeID >= len(s.nodes) || s.nodes[nodeID] == nil || s.nodes[nodeID].FullPath != path {
		return TooltipDetails{}, fmt.Errorf("selected item is no longer available")
	}
	node := s.nodes[nodeID]
	details := TooltipDetails{Incomplete: node.ScanIncomplete}
	if !node.IsFolder {
		return details, nil
	}
	details.RecursiveFiles, details.RecursiveFolders = subtreeEntryCounts(node)
	details.RecursiveFolders = max(0, details.RecursiveFolders-1)
	details.DirectFiles = details.RecursiveFiles
	for _, child := range node.Children {
		if child.IsFolder && !child.IsFreeSpace {
			details.DirectFolders++
			files, _ := subtreeEntryCounts(child)
			details.DirectFiles -= files
		}
	}
	details.DirectFiles = max(0, details.DirectFiles)
	return details, nil
}
