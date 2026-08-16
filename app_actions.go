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

	result, err := a.store.DeleteNode(nodeID, a.desktop.MoveToTrash)
	if err != nil {
		return DeleteResult{}, err
	}
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
	return result, nil
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
