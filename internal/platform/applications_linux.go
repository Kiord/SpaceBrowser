//go:build linux

package platform

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func linuxDefaultDesktopID(mimeType string) (string, error) {
	output, err := exec.Command("xdg-mime", "query", "default", mimeType).Output()
	if err != nil {
		return "", err
	}
	id := strings.TrimSpace(string(output))
	if id == "" || filepath.Base(id) != id || !strings.HasSuffix(id, ".desktop") {
		return "", fmt.Errorf("no default application for %s", mimeType)
	}
	return id, nil
}

func linuxDesktopApplicationPath(id string) (string, error) {
	if id == "" || filepath.Base(id) != id {
		return "", fmt.Errorf("invalid desktop application ID")
	}
	home := os.Getenv("XDG_DATA_HOME")
	if home == "" {
		userHome, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		home = filepath.Join(userHome, ".local", "share")
	}
	dirs := os.Getenv("XDG_DATA_DIRS")
	if dirs == "" {
		dirs = "/usr/local/share:/usr/share"
	}
	for _, root := range append([]string{home}, filepath.SplitList(dirs)...) {
		path := filepath.Join(root, "applications", id)
		if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() {
			return path, nil
		}
	}
	return "", fmt.Errorf("desktop application %s is unavailable", id)
}

func (Linux) DefaultApplicationPath(path string) (string, error) {
	output, err := exec.Command("xdg-mime", "query", "filetype", path).Output()
	if err != nil {
		return "", err
	}
	id, err := linuxDefaultDesktopID(strings.TrimSpace(string(output)))
	if err != nil {
		return "", err
	}
	return linuxDesktopApplicationPath(id)
}

func (Linux) FileBrowserApplication() (Application, error) {
	id, err := linuxDefaultDesktopID("inode/directory")
	if err != nil {
		return Application{}, err
	}
	name := linuxDesktopApplicationName(id)
	if name == "" {
		name = strings.TrimSuffix(id, ".desktop")
	}
	path, _ := linuxDesktopApplicationPath(id)
	return Application{Name: name, Path: path}, nil
}
