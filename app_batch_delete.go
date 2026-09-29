package main

import (
	"fmt"
	"path/filepath"

	"spacebrowser/internal/platform"
)

// Bind confirmation to a path and deletion mode, not just a recyclable node ID.
type DeletionTarget struct {
	NodeID int    `json:"nodeId"`
	Path   string `json:"path"`
	Action string `json:"action"`
}

type DeletionFailure struct {
	Path  string `json:"path"`
	Error string `json:"error"`
}

type BatchDeleteResult struct {
	DeleteResult
	Deleted  []int             `json:"deleted"`
	Failures []DeletionFailure `json:"failures"`
}

func (a *App) DeleteNodes(targets []DeletionTarget) (BatchDeleteResult, error) {
	a.filesystemMu.Lock()
	defer a.filesystemMu.Unlock()
	a.scanMu.RLock()
	defer a.scanMu.RUnlock()
	profile := a.GetProfile()
	if !profile.AllowDelete {
		return BatchDeleteResult{}, fmt.Errorf("delete commands are disabled; enable Allow delete command in Settings")
	}
	if a.scanActive {
		return BatchDeleteResult{}, fmt.Errorf("items cannot be deleted while a scan is running")
	}
	if len(targets) == 0 {
		return BatchDeleteResult{}, fmt.Errorf("no items selected")
	}

	// Validate every target before performing any filesystem mutation.
	nodes := make([]Node, len(targets))
	for i, target := range targets {
		path, err := a.store.NodePath(target.NodeID)
		if err != nil || filepath.Clean(path) != filepath.Clean(target.Path) || target.Path == "" {
			return BatchDeleteResult{}, fmt.Errorf("the selection changed; select the items again")
		}
		empty := a.desktop.IsTrashRoot(path)
		inTrash := !empty && a.desktop.IsInTrash(path)
		action := "trash"
		if empty {
			action = "empty"
		} else if inTrash || profile.AllowPermanentDelete {
			action = "permanent"
		}
		if target.Action != action {
			return BatchDeleteResult{}, fmt.Errorf("deletion settings changed; confirm the selection again")
		}
		if empty && len(targets) > 1 {
			return BatchDeleteResult{}, fmt.Errorf("empty Trash separately from other selected items")
		}
		mutation, err := a.store.prepareFilesystemMutation(target.NodeID, empty, false)
		if err != nil {
			return BatchDeleteResult{}, err
		}
		nodes[i] = mutation.node
		if !empty && !inTrash {
			if validator, ok := a.desktop.(platform.DeletionValidator); ok {
				if err := validator.ValidateDeletion(path); err != nil {
					return BatchDeleteResult{}, a.logDeletionError(err)
				}
			}
		}
	}
	kept := make([]DeletionTarget, 0, len(targets))
	seen := make(map[string]bool)
	for i, target := range targets {
		key := canonicalCachePath(target.Path)
		if seen[key] {
			continue
		}
		seen[key] = true
		covered := false
		for j, parent := range nodes {
			if j != i && parent.IsFolder && canonicalCachePath(parent.FullPath) != key && cachePathWithin(target.Path, parent.FullPath) {
				covered = true
				break
			}
		}
		if !covered {
			kept = append(kept, target)
		}
	}
	result := BatchDeleteResult{Deleted: []int{}, Failures: []DeletionFailure{}}
	trashTargets := make(map[int]trashRefreshTarget)
	for _, target := range kept {
		// A targeted Trash refresh is deferred until all original IDs are used.
		path, err := a.store.NodePath(target.NodeID)
		if err == nil && filepath.Clean(path) != filepath.Clean(target.Path) {
			err = fmt.Errorf("selected path changed")
		}
		var single DeleteResult
		if err == nil {
			single, err = a.deleteNodeLocked(target.NodeID, profile, true)
		}
		if err != nil {
			result.Failures = append(result.Failures, DeletionFailure{target.Path, err.Error()})
			// A native operation can fail after partially changing a directory.
			result.RescanRequired = true
			if a.scanCache != nil {
				a.scanCache.InvalidatePath(target.Path)
			}
			continue
		}
		result.Deleted = append(result.Deleted, target.NodeID)
		result.RescanRequired = result.RescanRequired || single.RescanRequired
		for _, refresh := range single.trashRefreshes {
			trashTargets[refresh.NodeID] = refresh
		}
	}
	result.FileCount, result.DirCount = a.store.Counts()
	if len(result.Deleted) > 0 {
		for _, refresh := range trashTargets {
			result.trashRefreshes = append(result.trashRefreshes, refresh)
		}
		result.DeleteResult = a.finishDeletion(result.DeleteResult, profile)
	}
	return result, nil
}
