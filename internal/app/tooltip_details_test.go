package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestTooltipFieldsPersistAndMigrate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	profile := *defaultProfile()
	if profile.TooltipFields != defaultTooltipFields() {
		t.Fatal("unexpected defaults")
	}
	for _, fields := range []TooltipFields{{}, {CreationDate: true, Counts: true}, defaultTooltipFields()} {
		profile.TooltipFields = fields
		if err := saveSettings(path, profile); err != nil {
			t.Fatal(err)
		}
		loaded, err := loadSettings(path)
		if err != nil {
			t.Fatal(err)
		}
		if loaded.TooltipFields != fields {
			t.Fatalf("fields = %+v, want %+v", loaded.TooltipFields, fields)
		}
	}
	data, _ := os.ReadFile(path)
	var legacy map[string]json.RawMessage
	if err := json.Unmarshal(data, &legacy); err != nil {
		t.Fatal(err)
	}
	legacy["version"] = json.RawMessage("16")
	delete(legacy, "tooltipFields")
	data, _ = json.Marshal(legacy)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	loaded, err := loadSettings(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.TooltipFields != defaultTooltipFields() {
		t.Fatalf("migration = %+v", loaded.TooltipFields)
	}
	legacy["version"] = json.RawMessage("17")
	legacy["tooltipFields"] = json.RawMessage(`{"icon":true,"directFiles":true,"creationDate":true}`)
	data, _ = json.Marshal(legacy)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	loaded, err = loadSettings(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.TooltipFields != (TooltipFields{Icon: true, Counts: true, CreationDate: true}) {
		t.Fatalf("combined counts migration = %+v", loaded.TooltipFields)
	}
}

func TestTooltipCountsIncludeSmallFilesAndNestedDirectories(t *testing.T) {
	path := t.TempDir()
	for _, dir := range []string{"child", "child/nested", "empty"} {
		if err := os.MkdirAll(filepath.Join(path, dir), 0700); err != nil {
			t.Fatal(err)
		}
	}
	for _, file := range []string{"a", "b", "child/c", "child/nested/d"} {
		if err := os.WriteFile(filepath.Join(path, file), []byte("small"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	profile := *defaultProfile()
	scanner := NewScanner(&profile, 1)
	var files, dirs int64
	root, err := scanner.buildTree(path, 0, -1, &files, &dirs)
	if err != nil {
		t.Fatal(err)
	}
	app := newApp("")
	app.store.Replace(root, scanner.Nodes(), int(files), int(dirs))
	details, err := app.GetTooltipDetails(root.ID, path, false)
	if err != nil {
		t.Fatal(err)
	}
	want := TooltipDetails{RecursiveFiles: 4, RecursiveFolders: 3, DirectFiles: 2, DirectFolders: 2}
	if details != want {
		t.Fatalf("details = %+v, want %+v", details, want)
	}
	if _, err := app.GetTooltipDetails(root.ID, path+"-stale", false); err == nil {
		t.Fatal("accepted stale path")
	}
	if _, err := app.GetTooltipDetails(-1, path, false); err == nil {
		t.Fatal("accepted synthetic node")
	}
}

func TestTooltipDirectFileCountsRetainDeduplicatedEntries(t *testing.T) {
	child := &Node{ID: 1, FullPath: "/root/child", IsFolder: true, EntryFiles: 5, EntryDirs: 1}
	root := &Node{ID: 0, FullPath: "/root", IsFolder: true, EntryFiles: 9, EntryDirs: 2, Children: []*Node{child}}
	store := TreeStore{}
	store.Replace(root, []*Node{root, child}, 9, 2)
	details, err := store.tooltipDetails(0, "/root")
	if err != nil {
		t.Fatal(err)
	}
	if details.DirectFiles != 4 || details.RecursiveFiles != 9 || details.DirectFolders != 1 || details.RecursiveFolders != 1 {
		t.Fatalf("details = %+v", details)
	}
}
