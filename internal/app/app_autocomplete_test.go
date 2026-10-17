package app

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFolderAutocomplete(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"alpha", "alpine", "beta"} {
		if err := os.Mkdir(filepath.Join(root, name), 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "also.txt"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	app := &App{}
	paths := app.SuggestFolders(filepath.Join(root, "al"))
	if len(paths) != 2 || paths[0] != filepath.Join(root, "alpha")+string(filepath.Separator) || paths[1] != filepath.Join(root, "alpine")+string(filepath.Separator) {
		t.Fatalf("unexpected suggestions: %v", paths)
	}
	if len(app.SuggestFolders(root+string(filepath.Separator))) != 3 {
		t.Fatal("directory completion omitted folders")
	}
	if len(app.SuggestFolders(filepath.Join(root, "missing")+string(filepath.Separator))) != 0 {
		t.Fatal("missing directory returned suggestions")
	}
	if len(app.SuggestFolders("")) != 0 {
		t.Fatal("empty input should not enumerate directories")
	}
}
