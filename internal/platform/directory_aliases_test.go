package platform

import (
	"path/filepath"
	"reflect"
	"testing"
)

func TestParseFirmlinkAliases(t *testing.T) {
	root := t.TempDir()
	data := filepath.Join(root, "System", "Volumes", "Data")
	aliases := parseFirmlinkAliases("# source target\n/Users\tUsers\r\n/Library Library\n/usr/local usr/local\n", root, data)
	want := []DirectoryAlias{
		{Preferred: filepath.Join(root, "Users"), Backing: filepath.Join(data, "Users")},
		{Preferred: filepath.Join(root, "Library"), Backing: filepath.Join(data, "Library")},
		{Preferred: filepath.Join(root, "usr", "local"), Backing: filepath.Join(data, "usr", "local")},
	}
	if !reflect.DeepEqual(aliases, want) {
		t.Fatalf("aliases = %#v, want %#v", aliases, want)
	}
}

func TestParseFirmlinkAliasesRejectsInvalidMappings(t *testing.T) {
	for _, line := range []string{
		"/Users", "/Users Users extra", "/ /", "/ .", "/Users /Users",
		"/../Users Users", "/Users ../Users", "/Users Users/../Library", ". Users",
	} {
		t.Run(line, func(t *testing.T) {
			if aliases := parseFirmlinkAliases(line, t.TempDir(), t.TempDir()); len(aliases) != 0 {
				t.Fatalf("invalid mapping produced aliases: %#v", aliases)
			}
		})
	}
}
