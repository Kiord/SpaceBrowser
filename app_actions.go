package main

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/shirou/gopsutil/v3/disk"
	"github.com/wailsapp/wails/v2/pkg/runtime"

	"spacebrowser/internal/fileicon"
	"spacebrowser/internal/platform"
)

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

	var result DeleteResult
	var err error
	if a.store.NodePathMatches(nodeID, a.desktop.IsTrashRoot) {
		result, err = a.store.EmptyTrashNode(nodeID, a.desktop.IsTrashRoot, a.desktop.EmptyTrash)
	} else if a.store.NodePathMatches(nodeID, a.desktop.IsInTrash) {
		if !profile.AllowPermanentDelete {
			return DeleteResult{}, fmt.Errorf("permanent deletion is disabled; enable Allow permanent deletion in Settings")
		}
		path, pathErr := a.store.NodePath(nodeID)
		if pathErr != nil {
			return DeleteResult{}, pathErr
		}
		if err = a.desktop.DeleteTrashItemPermanently(path); err == nil {
			files, dirs := a.store.Counts()
			result = DeleteResult{FileCount: files, DirCount: dirs, RescanRequired: true}
		}
	} else {
		result, err = a.store.DeleteNode(nodeID, a.desktop.IsTrashRoot, a.desktop.IsInTrash, a.desktop.MoveToTrash)
	}
	if err != nil {
		return DeleteResult{}, err
	}
	if len(result.trashRefreshes) > 0 {
		if profile.RescanOnDelete || result.RescanRequired {
			// The frontend will perform a full scan, so avoid scanning displayed
			// Trash subtrees only to discard those results immediately afterward.
			result.trashRefreshes = nil
		} else {
			result = a.refreshDisplayedTrash(result)
		}
	}
	a.refreshDiskUsageAfterFilesystemChange(&result)
	return result, nil
}

func (a *App) refreshDisplayedTrash(result DeleteResult) DeleteResult {
	requiresFullRescan := result.RescanRequired
	profile := a.GetProfile()
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	for _, target := range result.trashRefreshes {
		var files, dirs int64
		scanner := NewScannerWithFilesystem(&profile, 0, a.filesystem)
		scanner.SetContext(ctx, nil)
		root, err := scanner.buildTree(target.Path, 0, -1, &files, &dirs)
		if err != nil {
			if a.logger != nil {
				a.logger.Warningf("targeted Trash refresh failed for %s: %v", target.Path, err)
			}
			requiresFullRescan = true
			continue
		}
		refreshed, err := a.store.ReplaceSubtree(target.NodeID, root, int(files), int(dirs))
		if err != nil {
			if a.logger != nil {
				a.logger.Warningf("could not publish targeted Trash refresh for %s: %v", target.Path, err)
			}
			requiresFullRescan = true
			continue
		}
		result.FileCount = refreshed.FileCount
		result.DirCount = refreshed.DirCount
		requiresFullRescan = requiresFullRescan || refreshed.RescanRequired
		report := scanner.Report()
		if report.TotalErrors() > 0 && a.logger != nil {
			a.logger.Warningf("targeted Trash refresh for %s completed with %d filesystem or metadata errors", target.Path, report.TotalErrors())
		}
	}
	result.RescanRequired = requiresFullRescan
	result.trashRefreshes = nil
	return result
}

type TrashRestoreDetails struct {
	OriginalPath string `json:"originalPath"`
}

func (a *App) GetTrashRestoreInfo(nodeID int) (TrashRestoreDetails, error) {
	path, err := a.store.NodePath(nodeID)
	if err != nil {
		return TrashRestoreDetails{}, err
	}
	if !a.desktop.IsInTrash(path) || a.desktop.IsTrashRoot(path) {
		return TrashRestoreDetails{}, fmt.Errorf("the selected item is not restorable Trash content")
	}
	info, err := a.desktop.TrashRestoreInfo(path)
	if err != nil {
		return TrashRestoreDetails{}, err
	}
	return TrashRestoreDetails{OriginalPath: info.OriginalPath}, nil
}

func (a *App) RestoreNode(nodeID int) (DeleteResult, error) {
	a.scanMu.RLock()
	defer a.scanMu.RUnlock()
	if a.scanActive {
		return DeleteResult{}, fmt.Errorf("items cannot be restored while a scan is running")
	}
	path, err := a.store.NodePath(nodeID)
	if err != nil {
		return DeleteResult{}, err
	}
	if !a.desktop.IsInTrash(path) || a.desktop.IsTrashRoot(path) {
		return DeleteResult{}, fmt.Errorf("the selected item is not restorable Trash content")
	}
	if err := a.desktop.RestoreTrashItem(path); err != nil {
		return DeleteResult{}, err
	}
	files, dirs := a.store.Counts()
	result := DeleteResult{FileCount: files, DirCount: dirs, RescanRequired: true}
	a.refreshDiskUsageAfterFilesystemChange(&result)
	return result, nil
}

func (a *App) refreshDiskUsageAfterFilesystemChange(result *DeleteResult) {
	if rootPath, hasFreeSpace := a.store.DiskUsageRootPath(); hasFreeSpace {
		usage, usageErr := disk.Usage(rootPath)
		if usageErr != nil {
			result.RescanRequired = true
			if a.logger != nil {
				a.logger.Warningf("could not refresh disk usage after deletion: %v", usageErr)
			}
		} else {
			a.store.UpdateDiskUsage(int64(usage.Total), int64(usage.Free))
		}
	}
}

func (a *App) OpenInFileBrowser(path string) error {
	if path == "" {
		return fmt.Errorf("missing path")
	}
	return a.desktop.OpenInFileBrowser(path)
}

func (a *App) OpenPath(path string) error {
	if path == "" {
		return fmt.Errorf("missing path")
	}
	return a.desktop.OpenPath(path)
}

func (a *App) GetDefaultApplicationName(path string) (string, error) {
	if err := validateExistingPath(path); err != nil {
		return "", err
	}
	return a.desktop.DefaultApplicationName(path)
}

func (a *App) OpenWith(path string) error {
	if err := validateExistingPath(path); err != nil {
		return err
	}
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return a.desktop.OpenWith(ctx, path)
}

func (a *App) ShowProperties(path string) error {
	if err := validateExistingPath(path); err != nil {
		return err
	}
	return a.desktop.ShowProperties(path)
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
	a.iconServiceOnce.Do(func() {
		a.iconService = fileicon.NewService()
	})
	return a.iconService.DataURL(path, isFolder)
}

type ScanLocation struct {
	Name    string `json:"name"`
	Path    string `json:"path"`
	Kind    string `json:"kind"`
	IconURL string `json:"iconUrl"`
}

func (a *App) GetScanLocations() ([]ScanLocation, error) {
	locations, err := a.locations.ListScanLocations()
	if err != nil {
		return nil, err
	}
	result := make([]ScanLocation, 0, len(locations))
	for _, location := range locations {
		entry := ScanLocation{Name: location.Name, Path: location.Path, Kind: location.Kind}
		if iconURL, iconErr := a.GetAssociatedIcon(location.Path, true); iconErr == nil {
			entry.IconURL = iconURL
		} else if a.logger != nil {
			a.logger.Debugf("native location icon unavailable for %s: %v", location.Path, iconErr)
		}
		result = append(result, entry)
	}
	return result, nil
}

func (a *App) DefaultPath() string {
	return a.desktop.DefaultStartPath()
}

func (a *App) PickFolder() (string, error) {
	if a.ctx == nil {
		return "", fmt.Errorf("app not initialized")
	}
	const title = "Choose a folder to analyze"
	path, err := a.desktop.PickFolder(a.ctx, title)
	if errors.Is(err, platform.ErrOperationCancelled) {
		return "", nil
	}
	if err == nil {
		if path == "" {
			return "", nil
		}
		return validateScanPathWithFilesystem(path, a.filesystem)
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
	return validateScanPathWithFilesystem(path, a.filesystem)
}
