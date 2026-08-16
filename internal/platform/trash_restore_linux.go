//go:build linux

package platform

import (
	"bufio"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

func linuxTrashRootContaining(p string) (clean, root string, err error) {
	clean, err = filepath.Abs(p)
	if err != nil {
		return "", "", err
	}
	clean = filepath.Clean(clean)
	linuxPlatform := Linux{}
	for current := clean; ; current = filepath.Dir(current) {
		if linuxPlatform.IsTrashRoot(current) {
			return clean, current, nil
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", "", fmt.Errorf("path is not inside a supported Trash")
		}
	}
}

func linuxTrashRelativeParts(clean, root string) ([]string, error) {
	relative, err := filepath.Rel(root, clean)
	if err != nil || relative == "." || relative == "" || relative == ".." || strings.HasPrefix(relative, ".."+string(os.PathSeparator)) {
		return nil, fmt.Errorf("path is not a Trash item")
	}
	return strings.Split(relative, string(os.PathSeparator)), nil
}

func (Linux) TrashRestoreInfo(p string) (TrashRestoreInfo, error) {
	clean, root, err := linuxTrashRootContaining(p)
	if err != nil {
		return TrashRestoreInfo{}, err
	}
	parts, err := linuxTrashRelativeParts(clean, root)
	if err != nil || len(parts) < 2 || parts[0] != "files" {
		return TrashRestoreInfo{}, fmt.Errorf("the selected path is internal Trash metadata")
	}
	if len(parts) != 2 {
		return TrashRestoreInfo{}, fmt.Errorf("select the top-level Trash item to restore it")
	}
	targetPath := filepath.Join(root, "files", parts[1])
	infoPath := filepath.Join(root, "info", parts[1]+".trashinfo")
	originalPath, err := readFreeDesktopTrashInfo(infoPath, root)
	if err != nil {
		return TrashRestoreInfo{}, err
	}
	return TrashRestoreInfo{TargetPath: targetPath, OriginalPath: originalPath}, nil
}

func (l Linux) RestoreTrashItem(p string) error {
	info, err := l.TrashRestoreInfo(p)
	if err != nil {
		return err
	}
	if _, err := os.Lstat(info.OriginalPath); err == nil {
		return fmt.Errorf("the original location already exists: %s", info.OriginalPath)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("inspect original location: %w", err)
	}
	parent := filepath.Dir(info.OriginalPath)
	if parentInfo, err := os.Stat(parent); err != nil || !parentInfo.IsDir() {
		return fmt.Errorf("the original parent folder is unavailable: %s", parent)
	}
	if err := os.Rename(info.TargetPath, info.OriginalPath); err != nil {
		return fmt.Errorf("restore Trash item: %w", err)
	}
	_, root, _ := linuxTrashRootContaining(p)
	_ = os.Remove(filepath.Join(root, "info", filepath.Base(info.TargetPath)+".trashinfo"))
	return nil
}

func (Linux) DeleteTrashItemPermanently(p string) error {
	clean, root, err := linuxTrashRootContaining(p)
	if err != nil {
		return err
	}
	parts, err := linuxTrashRelativeParts(clean, root)
	if err != nil || len(parts) < 2 {
		return fmt.Errorf("the selected path is a protected Trash container")
	}

	switch parts[0] {
	case "files":
		if len(parts) > 2 {
			if err := os.RemoveAll(clean); err != nil {
				return fmt.Errorf("permanently delete Trash item: %w", err)
			}
			return nil
		}
		dataPath := filepath.Join(root, "files", parts[1])
		infoPath := filepath.Join(root, "info", parts[1]+".trashinfo")
		if err := os.RemoveAll(dataPath); err != nil {
			return fmt.Errorf("permanently delete Trash item: %w", err)
		}
		if err := os.Remove(infoPath); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove Trash metadata: %w", err)
		}
		return nil
	case "info":
		if len(parts) != 2 || !strings.HasSuffix(parts[1], ".trashinfo") {
			return fmt.Errorf("the selected path is protected Trash metadata")
		}
		name := strings.TrimSuffix(parts[1], ".trashinfo")
		if err := os.RemoveAll(filepath.Join(root, "files", name)); err != nil {
			return fmt.Errorf("permanently delete Trash item: %w", err)
		}
		if err := os.Remove(clean); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove Trash metadata: %w", err)
		}
		return nil
	default:
		return fmt.Errorf("the selected path is protected Trash metadata")
	}
}

func readFreeDesktopTrashInfo(path, trashRoot string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("read Trash metadata: %w", err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	if !scanner.Scan() || strings.TrimSpace(scanner.Text()) != "[Trash Info]" {
		return "", fmt.Errorf("Trash metadata has an invalid header")
	}
	var encodedPath string
	for scanner.Scan() {
		line := scanner.Text()
		if encodedPath == "" && strings.HasPrefix(line, "Path=") {
			encodedPath = strings.TrimPrefix(line, "Path=")
		}
	}
	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("read Trash metadata: %w", err)
	}
	decodedPath, err := url.PathUnescape(encodedPath)
	if err != nil || decodedPath == "" {
		return "", fmt.Errorf("Trash metadata has an invalid original path")
	}
	if filepath.IsAbs(decodedPath) {
		return filepath.Clean(decodedPath), nil
	}
	for _, component := range strings.Split(filepath.Clean(decodedPath), string(os.PathSeparator)) {
		if component == ".." {
			return "", fmt.Errorf("Trash metadata contains an unsafe relative path")
		}
	}
	base := filepath.Dir(trashRoot)
	if filepath.Base(base) == ".Trash" {
		base = filepath.Dir(base)
	}
	return filepath.Clean(filepath.Join(base, decodedPath)), nil
}
