//go:build windows

package platform

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

func (Windows) ValidateDeletion(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("inspect deletion target: %w", err)
	}
	clean := filepath.Clean(path)
	pathPointer, err := windows.UTF16PtrFromString(clean)
	if err != nil {
		return fmt.Errorf("inspect deletion target: %w", err)
	}
	attributes, err := windows.GetFileAttributes(pathPointer)
	if err != nil {
		return fmt.Errorf("inspect deletion target attributes: %w", err)
	}
	if attributes&windows.FILE_ATTRIBUTE_SYSTEM != 0 {
		return fmt.Errorf("%s", protectedDeletionMessage)
	}
	var mounts []string
	if info.IsDir() && info.Mode()&os.ModeSymlink == 0 {
		mounts, err = windowsVolumeMountPoints()
		if err != nil {
			return fmt.Errorf("SpaceBrowser could not verify mounted filesystems, so deletion was blocked: %w", err)
		}
	}

	protectedTrees := compactPaths(
		os.Getenv("SystemRoot"),
		os.Getenv("ProgramFiles"),
		os.Getenv("ProgramFiles(x86)"),
		os.Getenv("ProgramData"),
	)
	systemDrive := os.Getenv("SystemDrive")
	if systemDrive == "" {
		systemDrive = filepath.VolumeName(os.Getenv("SystemRoot"))
	}
	if systemDrive != "" {
		root := systemDrive + `\`
		protectedTrees = append(protectedTrees,
			filepath.Join(root, "Recovery"),
			filepath.Join(root, "Boot"),
			filepath.Join(root, "EFI"),
			filepath.Join(root, "System Volume Information"),
			filepath.Join(root, "$WinREAgent"),
		)
	}
	protectedExact := []string{}
	if systemDrive != "" {
		root := systemDrive + `\`
		protectedExact = append(protectedExact,
			root,
			filepath.Join(root, "Users"),
			filepath.Join(root, "Documents and Settings"),
			filepath.Join(root, "pagefile.sys"),
			filepath.Join(root, "hiberfil.sys"),
			filepath.Join(root, "swapfile.sys"),
			filepath.Join(root, "bootmgr"),
			filepath.Join(root, "BOOTNXT"),
		)
	}
	return validateDeletionTarget(path, info, protectedTrees, protectedExact, mounts, true)
}

func compactPaths(paths ...string) []string {
	result := make([]string, 0, len(paths))
	for _, path := range paths {
		if strings.TrimSpace(path) != "" {
			result = append(result, filepath.Clean(path))
		}
	}
	return result
}

func windowsVolumeMountPoints() ([]string, error) {
	volumeBuffer := make([]uint16, windows.MAX_PATH+1)
	handle, err := windows.FindFirstVolume(&volumeBuffer[0], uint32(len(volumeBuffer)))
	if err != nil {
		return nil, err
	}
	defer windows.FindVolumeClose(handle)

	var result []string
	for {
		volumeName := windows.UTF16ToString(volumeBuffer)
		paths, pathsErr := windowsPathsForVolume(volumeName)
		if pathsErr != nil {
			return nil, pathsErr
		}
		result = append(result, paths...)

		for index := range volumeBuffer {
			volumeBuffer[index] = 0
		}
		err = windows.FindNextVolume(handle, &volumeBuffer[0], uint32(len(volumeBuffer)))
		if errors.Is(err, windows.ERROR_NO_MORE_FILES) {
			break
		}
		if err != nil {
			return nil, err
		}
	}
	return result, nil
}

func windowsPathsForVolume(volumeName string) ([]string, error) {
	volumePointer, err := windows.UTF16PtrFromString(volumeName)
	if err != nil {
		return nil, err
	}
	buffer := make([]uint16, windows.MAX_PATH+1)
	for {
		var required uint32
		err = windows.GetVolumePathNamesForVolumeName(volumePointer, &buffer[0], uint32(len(buffer)), &required)
		if errors.Is(err, windows.ERROR_MORE_DATA) {
			buffer = make([]uint16, required)
			continue
		}
		if err != nil {
			return nil, err
		}
		break
	}
	var result []string
	for start := 0; start < len(buffer) && buffer[start] != 0; {
		end := start
		for end < len(buffer) && buffer[end] != 0 {
			end++
		}
		result = append(result, windows.UTF16ToString(buffer[start:end]))
		start = end + 1
	}
	return result, nil
}
