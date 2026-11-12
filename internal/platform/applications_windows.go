//go:build windows

package platform

import (
	"fmt"
	"path/filepath"
	"strings"
)

func (Windows) DefaultApplicationPath(path string) (string, error) {
	extension := filepath.Ext(path)
	if extension == "" {
		return "", fmt.Errorf("this file has no extension association")
	}
	return queryAssociation(extension, assocStrExecutable)
}

func (Windows) FileBrowserApplication() (Application, error) {
	for _, class := range []string{"Directory", "Folder"} {
		path, err := queryAssociation(class, assocStrExecutable)
		if err != nil || strings.TrimSpace(path) == "" {
			continue
		}
		name, _ := queryAssociation(class, assocStrFriendlyAppName)
		if strings.EqualFold(filepath.Base(path), "explorer.exe") {
			name = "File Explorer"
		} else if strings.TrimSpace(name) == "" {
			name = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
		}
		return Application{Name: strings.TrimSpace(name), Path: path}, nil
	}
	return Application{}, fmt.Errorf("the default file manager could not be resolved")
}
