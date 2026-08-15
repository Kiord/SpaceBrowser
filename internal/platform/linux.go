//go:build linux

package platform

import (
	"fmt"
	"github.com/godbus/dbus/v5"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
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
		uri := "file://" + filepath.ToSlash(p)
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

func (Linux) OpenWith(p string) error {
	file, err := os.Open(p)
	if err != nil {
		return fmt.Errorf("open item for application chooser: %w", err)
	}
	defer file.Close()

	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return fmt.Errorf("connect to desktop application portal: %w", err)
	}
	defer conn.Close()

	match := []dbus.MatchOption{
		dbus.WithMatchInterface("org.freedesktop.portal.Request"),
		dbus.WithMatchMember("Response"),
	}
	responses := make(chan *dbus.Signal, 8)
	conn.Signal(responses)
	defer conn.RemoveSignal(responses)
	if err := conn.AddMatchSignal(match...); err != nil {
		return fmt.Errorf("listen for application selector response: %w", err)
	}
	defer conn.RemoveMatchSignal(match...)

	options := map[string]dbus.Variant{"ask": dbus.MakeVariant(true)}
	portal := conn.Object("org.freedesktop.portal.Desktop", dbus.ObjectPath("/org/freedesktop/portal/desktop"))
	var requestPath dbus.ObjectPath
	if err := portal.Call(
		"org.freedesktop.portal.OpenURI.OpenFile",
		0,
		"",
		dbus.UnixFD(file.Fd()),
		options,
	).Store(&requestPath); err != nil {
		return fmt.Errorf("open application selector: %w", err)
	}

	for signal := range responses {
		if signal == nil || signal.Name != "org.freedesktop.portal.Request.Response" || signal.Path != requestPath {
			continue
		}
		var response uint32
		var results map[string]dbus.Variant
		if err := dbus.Store(signal.Body, &response, &results); err != nil {
			return fmt.Errorf("read application selector response: %w", err)
		}
		return linuxOpenWithResponseError(response)
	}
	return fmt.Errorf("application selector closed without a response")
}

func linuxOpenWithResponseError(response uint32) error {
	switch response {
	case 0, 1: // Success, or the user cancelled the chooser.
		return nil
	default:
		return fmt.Errorf("application selector failed (portal response %d)", response)
	}
}

func (Linux) ShowProperties(p string) error {
	if _, err := exec.LookPath("dbus-send"); err != nil {
		return fmt.Errorf("the desktop file manager properties service is unavailable")
	}
	uri := (&url.URL{Scheme: "file", Path: p}).String()
	if err := exec.Command("dbus-send", "--session",
		"--dest=org.freedesktop.FileManager1", "--type=method_call", "--print-reply",
		"/org/freedesktop/FileManager1", "org.freedesktop.FileManager1.ShowItemProperties",
		"array:string:"+uri, "string:").Run(); err != nil {
		return fmt.Errorf("show filesystem properties: %w", err)
	}
	return nil
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
