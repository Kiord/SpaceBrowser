//go:build linux

package platform

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"spacebrowser/internal/desktopportal"
)

type Linux struct{ Default }

func (Linux) UsageFor(_ string, fi os.FileInfo) FileUsage {
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		return FileUsage{
			AllocatedSize: int64(st.Blocks) * 512,
			Identity: FileIdentity{
				Volume: uint64(st.Dev),
				Low:    uint64(st.Ino),
			},
			HasIdentity:  true,
			LinkCount:    uint64(st.Nlink),
			HasLinkCount: true,
		}
	}
	return FileUsage{
		AllocatedSize: fi.Size(),
		LinkCount:     1,
		MetadataError: fmt.Errorf("native Linux file metadata is unavailable"),
	}
}
func (Linux) OpenInFileBrowser(p string) error {
	if info, err := os.Stat(p); err == nil && !info.IsDir() {
		uri := linuxFileURI(p)
		if err := exec.Command("dbus-send", "--session",
			"--dest=org.freedesktop.FileManager1", "--type=method_call", "--print-reply",
			"/org/freedesktop/FileManager1", "org.freedesktop.FileManager1.ShowItems",
			"array:string:"+uri, "string:").Run(); err == nil {
			return nil
		}
		return exec.Command("xdg-open", filepath.Dir(p)).Run()
	}
	return exec.Command("xdg-open", p).Run()
}

func (Linux) DefaultApplicationName(p string) (string, error) {
	if _, err := exec.LookPath("xdg-mime"); err != nil {
		return "", fmt.Errorf("xdg-mime is unavailable")
	}
	mimeOutput, err := exec.Command("xdg-mime", "query", "filetype", p).Output()
	if err != nil {
		return "", fmt.Errorf("detect file type: %w", err)
	}
	mimeType := strings.TrimSpace(string(mimeOutput))
	if mimeType == "" {
		return "", fmt.Errorf("the file type could not be detected")
	}
	applicationOutput, err := exec.Command("xdg-mime", "query", "default", mimeType).Output()
	if err != nil {
		return "", fmt.Errorf("find default application: %w", err)
	}
	desktopID := strings.TrimSpace(string(applicationOutput))
	if desktopID == "" {
		return "", fmt.Errorf("no default application is registered for %s", mimeType)
	}
	if name := linuxDesktopApplicationName(desktopID); name != "" {
		return name, nil
	}
	name := strings.TrimSuffix(filepath.Base(desktopID), ".desktop")
	name = strings.ReplaceAll(name, "-", " ")
	if name == "" {
		return "", fmt.Errorf("the default application has no display name")
	}
	return strings.ToUpper(name[:1]) + name[1:], nil
}

func linuxDesktopApplicationName(desktopID string) string {
	var dataRoots []string
	if dataHome := os.Getenv("XDG_DATA_HOME"); dataHome != "" {
		dataRoots = append(dataRoots, dataHome)
	} else if home, err := os.UserHomeDir(); err == nil {
		dataRoots = append(dataRoots, filepath.Join(home, ".local", "share"))
	}
	dataDirs := os.Getenv("XDG_DATA_DIRS")
	if dataDirs == "" {
		dataDirs = "/usr/local/share:/usr/share"
	}
	dataRoots = append(dataRoots, filepath.SplitList(dataDirs)...)
	for _, root := range dataRoots {
		contents, err := os.ReadFile(filepath.Join(root, "applications", desktopID))
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(contents), "\n") {
			if strings.HasPrefix(line, "Name=") {
				return strings.TrimSpace(strings.TrimPrefix(line, "Name="))
			}
		}
	}
	return ""
}

func (Linux) OpenWith(ctx context.Context, path string) error {
	client, err := desktopportal.Connect(ctx)
	if err != nil {
		return portalChooserError("application", err)
	}
	defer client.Close()

	err = client.OpenFile(ctx, path, desktopportal.OpenFileOptions{Ask: true})
	if errors.Is(err, desktopportal.ErrCancelled) {
		return nil
	}
	if err != nil {
		return portalChooserError("application", err)
	}
	return nil
}

func (Linux) PickFolder(ctx context.Context, title string) (string, error) {
	client, err := desktopportal.Connect(ctx)
	if err != nil {
		return "", portalFolderPickerError(err)
	}
	defer client.Close()

	path, err := client.PickDirectory(ctx, desktopportal.PickDirectoryOptions{Title: title})
	if errors.Is(err, desktopportal.ErrCancelled) {
		return "", ErrOperationCancelled
	}
	if err != nil {
		return "", portalFolderPickerError(err)
	}
	return path, nil
}

func portalChooserError(kind string, err error) error {
	if errors.Is(err, desktopportal.ErrUnavailable) || errors.Is(err, desktopportal.ErrUnsupported) {
		return fmt.Errorf("%s chooser unavailable: %w", kind, err)
	}
	return fmt.Errorf("%s chooser failed: %w", kind, err)
}

func portalFolderPickerError(err error) error {
	if errors.Is(err, desktopportal.ErrUnavailable) || errors.Is(err, desktopportal.ErrUnsupported) {
		return fmt.Errorf("%w: %v", ErrFolderPickerUnavailable, err)
	}
	return fmt.Errorf("folder chooser failed: %w", err)
}

func (Linux) ShowProperties(p string) error {
	if _, err := exec.LookPath("dbus-send"); err != nil {
		return fmt.Errorf("the desktop file manager properties service is unavailable")
	}
	uri := linuxFileURI(p)
	if err := exec.Command("dbus-send", "--session",
		"--dest=org.freedesktop.FileManager1", "--type=method_call", "--print-reply",
		"/org/freedesktop/FileManager1", "org.freedesktop.FileManager1.ShowItemProperties",
		"array:string:"+uri, "string:").Run(); err != nil {
		return fmt.Errorf("show filesystem properties: %w", err)
	}
	return nil
}

func linuxFileURI(path string) string {
	return (&url.URL{Scheme: "file", Path: filepath.ToSlash(path)}).String()
}

func (Linux) MoveToTrash(p string) error {
	commands := [][]string{
		{"gio", "trash", "--", p},
		{"kioclient6", "move", p, "trash:/"},
		{"kioclient5", "move", p, "trash:/"},
	}
	var lastErr error
	for _, command := range commands {
		if _, err := exec.LookPath(command[0]); err != nil {
			continue
		}
		if err := exec.Command(command[0], command[1:]...).Run(); err == nil {
			return nil
		} else {
			lastErr = err
		}
	}
	if lastErr != nil {
		return fmt.Errorf("move to Trash: %w", lastErr)
	}
	return fmt.Errorf("no desktop Trash service is available")
}

func (Linux) IsTrashRoot(p string) bool {
	dataHome, ok := linuxTrashConfiguration()
	if !ok {
		return false
	}
	return isLinuxTrashRoot(p, dataHome, os.Getuid(), (Linux{}).IsMountRoot)
}

func (l Linux) IsInTrash(p string) bool {
	dataHome, ok := linuxTrashConfiguration()
	if !ok {
		return false
	}
	return isLinuxPathInTrash(p, dataHome, os.Getuid(), l.IsMountRoot)
}

func linuxTrashConfiguration() (dataHome string, ok bool) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", false
	}
	dataHome = os.Getenv("XDG_DATA_HOME")
	if dataHome == "" || !filepath.IsAbs(dataHome) {
		dataHome = filepath.Join(home, ".local", "share")
	}
	return filepath.Clean(dataHome), true
}

func isLinuxTrashRoot(path, dataHome string, uid int, isMountRoot func(string) bool) bool {
	clean, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	clean = filepath.Clean(clean)
	uidText := strconv.Itoa(uid)
	if clean == filepath.Clean(filepath.Join(dataHome, "Trash")) {
		return linuxOwnedDirectory(clean, uid)
	}

	base := filepath.Base(clean)
	switch {
	case base == ".Trash-"+uidText:
		return isMountRoot(filepath.Dir(clean)) && linuxOwnedDirectory(clean, uid)
	case base == uidText && filepath.Base(filepath.Dir(clean)) == ".Trash":
		sharedTrash := filepath.Dir(clean)
		return isMountRoot(filepath.Dir(sharedTrash)) && linuxSafeSharedTrash(sharedTrash) && linuxOwnedDirectory(clean, uid)
	case base == ".Trash":
		return isMountRoot(filepath.Dir(clean)) && linuxSafeSharedTrash(clean) && linuxOwnedDirectory(filepath.Join(clean, uidText), uid)
	default:
		return false
	}
}

func isLinuxPathInTrash(path, dataHome string, uid int, isMountRoot func(string) bool) bool {
	clean, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	for current := filepath.Clean(clean); ; current = filepath.Dir(current) {
		if isLinuxTrashRoot(current, dataHome, uid, isMountRoot) {
			return true
		}
		parent := filepath.Dir(current)
		if parent == current {
			return false
		}
	}
}

func linuxOwnedDirectory(path string, uid int) bool {
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return false
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && int(stat.Uid) == uid
}

func linuxSafeSharedTrash(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && info.IsDir() && info.Mode()&os.ModeSymlink == 0 && info.Mode()&os.ModeSticky != 0
}

func (l Linux) EmptyTrash(p string) error {
	if !l.IsTrashRoot(p) {
		return fmt.Errorf("the selected folder is not a supported Trash root")
	}
	commands := [][]string{
		{"gio", "trash", "--empty"},
		{"ktrash6", "--empty"},
		{"ktrash5", "--empty"},
	}
	return emptyLinuxTrash(p, commands, func(command []string) (bool, error) {
		if _, err := exec.LookPath(command[0]); err != nil {
			return false, nil
		}
		return true, exec.Command(command[0], command[1:]...).Run()
	})
}

func emptyLinuxTrash(p string, commands [][]string, run func([]string) (bool, error)) error {
	roots := linuxUserTrashRoots(p)
	return emptyLinuxTrashLocations(roots, commands, run)
}

func emptyLinuxTrashLocations(roots []string, commands [][]string, run func([]string) (bool, error)) error {
	containsItems, err := linuxTrashRootsContainItems(roots)
	if err != nil {
		return fmt.Errorf("inspect Trash contents: %w", err)
	}
	if !containsItems {
		return nil
	}

	var commandErrors []error
	for _, command := range commands {
		attempted, commandErr := run(command)
		if !attempted {
			continue
		}
		if commandErr != nil {
			commandErrors = append(commandErrors, fmt.Errorf("%s: %w", command[0], commandErr))
		}
		containsItems, inspectErr := linuxTrashRootsContainItems(roots)
		if inspectErr != nil {
			return fmt.Errorf("verify Trash after %s: %w", command[0], inspectErr)
		}
		if !containsItems {
			return nil
		}
	}

	// Desktop helpers can return success without affecting another desktop's
	// Trash backend. The roots below have already passed authoritative
	// FreeDesktop location and ownership checks, so empty them directly.
	if err := emptyLinuxTrashRoots(roots); err != nil {
		commandErrors = append(commandErrors, err)
		return fmt.Errorf("empty Trash: %w", errors.Join(commandErrors...))
	}
	return nil
}

func linuxUserTrashRoots(selected string) []string {
	uid := os.Getuid()
	uidText := strconv.Itoa(uid)
	seen := make(map[string]struct{})
	roots := make([]string, 0, 4)
	add := func(path string) {
		clean := filepath.Clean(path)
		if filepath.Base(clean) == ".Trash" {
			clean = filepath.Join(clean, uidText)
		}
		if _, exists := seen[clean]; exists {
			return
		}
		seen[clean] = struct{}{}
		roots = append(roots, clean)
	}
	add(selected)

	dataHome, configured := linuxTrashConfiguration()
	if configured {
		homeTrash := filepath.Join(dataHome, "Trash")
		if linuxOwnedDirectory(homeTrash, uid) {
			add(homeTrash)
		}
	}

	mountInfo, err := os.Open(linuxMountInfoPath)
	if err != nil {
		return roots
	}
	defer mountInfo.Close()
	mounts := linuxMountPoints(mountInfo)
	isMountRoot := func(path string) bool {
		_, found := mounts[filepath.Clean(path)]
		return found
	}
	for mount := range mounts {
		for _, candidate := range []string{
			filepath.Join(mount, ".Trash-"+uidText),
			filepath.Join(mount, ".Trash", uidText),
		} {
			if configured && isLinuxTrashRoot(candidate, dataHome, uid, isMountRoot) {
				add(candidate)
			}
		}
	}
	return roots
}

func linuxTrashRootsContainItems(roots []string) (bool, error) {
	for _, root := range roots {
		for _, name := range []string{"files", "info"} {
			directory := filepath.Join(root, name)
			info, err := os.Lstat(directory)
			if os.IsNotExist(err) {
				continue
			}
			if err != nil {
				return false, fmt.Errorf("inspect %s: %w", directory, err)
			}
			if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
				return false, fmt.Errorf("unsafe Trash content directory %s", directory)
			}
			entries, err := os.ReadDir(directory)
			if err != nil {
				return false, fmt.Errorf("read %s: %w", directory, err)
			}
			if len(entries) > 0 {
				return true, nil
			}
		}
	}
	return false, nil
}

func emptyLinuxTrashRoots(roots []string) error {
	var failures []error
	for _, root := range roots {
		for _, name := range []string{"files", "info"} {
			directory := filepath.Join(root, name)
			info, err := os.Lstat(directory)
			if os.IsNotExist(err) {
				continue
			}
			if err != nil {
				failures = append(failures, fmt.Errorf("inspect %s: %w", directory, err))
				continue
			}
			if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
				failures = append(failures, fmt.Errorf("refusing unsafe Trash content directory %s", directory))
				continue
			}
			entries, err := os.ReadDir(directory)
			if err != nil {
				failures = append(failures, fmt.Errorf("read %s: %w", directory, err))
				continue
			}
			for _, entry := range entries {
				if err := os.RemoveAll(filepath.Join(directory, entry.Name())); err != nil {
					failures = append(failures, fmt.Errorf("remove %s: %w", filepath.Join(directory, entry.Name()), err))
				}
			}
		}
	}
	return errors.Join(failures...)
}

func (Linux) DefaultStartPath() string {
	if fi, err := os.Stat("/home"); err == nil && fi.IsDir() {
		return "/home"
	}
	if h, err := os.UserHomeDir(); err == nil {
		if fi, err := os.Stat(h); err == nil && fi.IsDir() {
			return h
		}
	}
	return "/"
}

const (
	NFS_SUPER_MAGIC    = 0x6969
	CIFS_SUPER_MAGIC   = 0xFF534D42
	SMB2_SUPER_MAGIC   = 0xFE534D42
	FUSE_SUPER_MAGIC   = 0x65735546
	AUTOFS_SUPER_MAGIC = 0x0187
)

func (Linux) IsLikelyNetworkFS(p string) bool {
	var st syscall.Statfs_t
	if err := syscall.Statfs(p, &st); err == nil {
		switch uint64(st.Type) {
		case NFS_SUPER_MAGIC, CIFS_SUPER_MAGIC, SMB2_SUPER_MAGIC, FUSE_SUPER_MAGIC, AUTOFS_SUPER_MAGIC:
			return true
		}
	}
	// user mounts
	if strings.HasPrefix(p, "/run/user/") && strings.Contains(p, "/gvfs/") {
		return true
	}
	return false
}

func init() { Impl = Linux{} }
