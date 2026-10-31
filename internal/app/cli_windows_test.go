package app

import (
	"encoding/xml"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

func TestWindowsBackgroundScanCommandPreservesCurrentFolder(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "packaging", "windows", "SpaceBrowser.wxs"))
	if err != nil {
		t.Fatal(err)
	}
	var installer struct {
		Components []struct {
			ID  string `xml:"Id,attr"`
			Key struct {
				Path   string `xml:"Key,attr"`
				Values []struct {
					Key   string `xml:"Key,attr"`
					Value string `xml:"Value,attr"`
				} `xml:"RegistryValue"`
			} `xml:"RegistryKey"`
		} `xml:"Package>StandardDirectory>Directory>Component"`
		Features []struct {
			ID   string `xml:"Id,attr"`
			Refs []struct {
				ID string `xml:"Id,attr"`
			} `xml:"ComponentRef"`
		} `xml:"Package>Feature"`
	}
	if err := xml.Unmarshal(data, &installer); err != nil {
		t.Fatal(err)
	}
	var command, componentID string
	for _, component := range installer.Components {
		if component.Key.Path == `Software\Classes\Directory\Background\shell\SpaceBrowser.Scan` {
			componentID = component.ID
			for _, value := range component.Key.Values {
				if value.Key == "command" {
					command = value.Value
				}
			}
		}
	}
	if command == "" {
		t.Fatal("installer has no folder-background scan command")
	}
	installed := false
	for _, feature := range installer.Features {
		if feature.ID == "ContextMenuFeature" {
			for _, ref := range feature.Refs {
				installed = installed || ref.ID == componentID
			}
		}
	}
	if !installed {
		t.Fatal("folder-background command is not included in the context menu feature")
	}
	const executable = `C:\Program Files\SpaceBrowser\spacebrowser.exe`
	for _, folder := range []string{`C:\`, `C:\a folder\`, `C:\a folder\fish & chips`, `C:\résumé\資料`, `C:\100% complete`, `\\server\share\`, `\\server\share\a folder`} {
		t.Run(folder, func(t *testing.T) {
			// Resolve MSI's executable and Explorer's current-folder substitution,
			// then use Windows' real argument parser before the application's CLI.
			line := strings.ReplaceAll(strings.ReplaceAll(command, "[#SpaceBrowserExecutable]", executable), "%V", folder)
			args, err := windows.DecomposeCommandLine(line)
			if err != nil {
				t.Fatal(err)
			}
			if len(args) != 3 || args[0] != executable {
				t.Fatalf("shell command arguments = %q", args)
			}
			options, err := parseCommandLine(args[1:])
			if err != nil {
				t.Fatal(err)
			}
			if filepath.Clean(options.initialPath) != filepath.Clean(folder) || options.disableCache {
				t.Fatalf("startup options = %+v, want current folder %q", options, folder)
			}
		})
	}
}
