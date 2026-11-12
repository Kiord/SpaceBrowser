//go:build darwin

package platform

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

func (Darwin) DefaultApplicationPath(path string) (string, error) {
	const script = `on run argv
set applicationAlias to default application of (info for POSIX file (item 1 of argv))
return POSIX path of applicationAlias
end run`
	output, err := exec.Command("osascript", "-e", script, path).Output()
	if err != nil {
		return "", fmt.Errorf("find default application path: %w", err)
	}
	return strings.TrimSpace(string(output)), nil
}

func (d Darwin) FileBrowserApplication() (Application, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Application{}, err
	}
	name, err := d.DefaultApplicationName(home)
	if err != nil {
		return Application{}, err
	}
	path, _ := d.DefaultApplicationPath(home)
	return Application{Name: name, Path: path}, nil
}
