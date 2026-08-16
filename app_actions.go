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
	a.refreshDiskUsageAfterFilesystemChange(&result)
	return result, nil
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
