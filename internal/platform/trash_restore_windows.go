//go:build windows

package platform

import (
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf16"
)

func windowsTrashPathParts(p string) (clean, root string, parts []string, err error) {
	clean = (Windows{}).Canonicalize(p)
	volume := filepath.VolumeName(clean)
	if len(volume) != 2 || volume[1] != ':' {
		return "", "", nil, fmt.Errorf("path is not on a drive-letter volume")
	}
	root = filepath.Clean(volume + `\$Recycle.Bin`)
	relative, relErr := filepath.Rel(root, clean)
	if relErr != nil || relative == "." || relative == "" || strings.HasPrefix(relative, `..\`) || relative == ".." {
		return "", "", nil, fmt.Errorf("path is not a Recycle Bin item")
	}
	parts = strings.Split(relative, string(os.PathSeparator))
	return clean, root, parts, nil
}

func windowsRecyclePair(root string, parts []string) (dataPath, infoPath string, direct bool, err error) {
	if len(parts) < 2 {
		return "", "", false, fmt.Errorf("the selected path is a Recycle Bin container")
	}
	name := parts[1]
	if len(name) < 2 || name[0] != '$' || (name[1] != 'R' && name[1] != 'r' && name[1] != 'I' && name[1] != 'i') {
		return "", "", false, fmt.Errorf("the selected path is internal Recycle Bin metadata")
	}
	suffix := name[2:]
	dataPath = filepath.Join(root, parts[0], "$R"+suffix)
	infoPath = filepath.Join(root, parts[0], "$I"+suffix)
	return dataPath, infoPath, len(parts) == 2, nil
}

func (Windows) TrashRestoreInfo(p string) (TrashRestoreInfo, error) {
	_, root, parts, err := windowsTrashPathParts(p)
	if err != nil {
		return TrashRestoreInfo{}, err
	}
	dataPath, infoPath, direct, err := windowsRecyclePair(root, parts)
	if err != nil {
		return TrashRestoreInfo{}, err
	}
	if !direct {
		return TrashRestoreInfo{}, fmt.Errorf("select the top-level Recycle Bin item to restore it")
	}
	originalPath, err := readWindowsRecycleInfo(infoPath)
	if err != nil {
		return TrashRestoreInfo{}, err
	}
	return TrashRestoreInfo{TargetPath: dataPath, OriginalPath: originalPath}, nil
}

func (w Windows) RestoreTrashItem(p string) error {
	info, err := w.TrashRestoreInfo(p)
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
		return fmt.Errorf("restore Recycle Bin item: %w", err)
	}
	_, root, parts, _ := windowsTrashPathParts(p)
	_, infoPath, _, _ := windowsRecyclePair(root, parts)
	_ = os.Remove(infoPath)
	return nil
}

func (Windows) DeleteTrashItemPermanently(p string) error {
	clean, root, parts, err := windowsTrashPathParts(p)
	if err != nil {
		return err
	}
	dataPath, infoPath, direct, pairErr := windowsRecyclePair(root, parts)
	if pairErr != nil {
		return pairErr
	}
	if !direct {
		if err := os.RemoveAll(clean); err != nil {
			return fmt.Errorf("permanently delete Recycle Bin item: %w", err)
		}
		return nil
	}
	if err := os.RemoveAll(dataPath); err != nil {
		return fmt.Errorf("permanently delete Recycle Bin item: %w", err)
	}
	if err := os.Remove(infoPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove Recycle Bin metadata: %w", err)
	}
	return nil
}

func readWindowsRecycleInfo(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read Recycle Bin metadata: %w", err)
	}
	if len(data) < 28 {
		return "", fmt.Errorf("Recycle Bin metadata is truncated")
	}
	version := binary.LittleEndian.Uint64(data[:8])
	var encoded []byte
	switch version {
	case 1:
		encoded = data[24:]
	case 2:
		characters := int(binary.LittleEndian.Uint32(data[24:28]))
		if characters <= 0 || characters > (len(data)-28)/2 {
			return "", fmt.Errorf("Recycle Bin metadata has an invalid path length")
		}
		encoded = data[28 : 28+characters*2]
	default:
		return "", fmt.Errorf("unsupported Recycle Bin metadata version %d", version)
	}
	units := make([]uint16, 0, len(encoded)/2)
	for index := 0; index+1 < len(encoded); index += 2 {
		unit := binary.LittleEndian.Uint16(encoded[index : index+2])
		if unit == 0 {
			break
		}
		units = append(units, unit)
	}
	originalPath := filepath.Clean(string(utf16.Decode(units)))
	if len(units) == 0 || !filepath.IsAbs(originalPath) {
		return "", fmt.Errorf("Recycle Bin metadata does not contain an absolute original path")
	}
	return originalPath, nil
}
