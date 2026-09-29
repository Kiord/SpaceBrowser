package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"spacebrowser/internal/platform"
)

type batchTestDesktop struct {
	platform.Default
	calls          []string
	blocked        string
	fail           string
	afterDelete    func()
	permanentCalls int
}

func (*batchTestDesktop) IsTrashRoot(string) bool { return false }
func (*batchTestDesktop) IsInTrash(string) bool   { return false }
func (d *batchTestDesktop) ValidateDeletion(path string) error {
	if path == d.blocked {
		return errors.New("protected location")
	}
	return nil
}
func (d *batchTestDesktop) MoveToTrash(path string) error {
	d.calls = append(d.calls, path)
	if path == d.fail {
		return errors.New("access denied")
	}
	if err := os.RemoveAll(path); err != nil {
		return err
	}
	if d.afterDelete != nil {
		d.afterDelete()
	}
	return nil
}
func (d *batchTestDesktop) DeletePermanently(string) error {
	d.permanentCalls++
	return errors.New("unexpected permanent deletion")
}

func batchDeleteFixture(t *testing.T) (*App, *batchTestDesktop, map[string]DeletionTarget) {
	t.Helper()
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "folder"), 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{filepath.Join("folder", "child"), "folder-other", "last"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("file"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	tree, nodes, files, dirs := scanTestTree(t, root, platform.Impl)
	app := newApp(filepath.Join(t.TempDir(), "settings.json"))
	t.Cleanup(func() { app.Shutdown(nil) })
	app.profile.AllowDelete = true
	app.profile.AllowPermanentDelete = false
	app.profile.RescanOnDelete = false
	desktop := &batchTestDesktop{}
	app.desktop = desktop
	app.store.Replace(tree, nodes, int(files), int(dirs))
	targets := make(map[string]DeletionTarget)
	for _, node := range nodes {
		targets[node.Name] = DeletionTarget{node.ID, node.FullPath, "trash"}
	}
	return app, desktop, targets
}

func TestBatchDeletionCollapsesDescendantsAndDuplicateTargets(t *testing.T) {
	app, desktop, targets := batchDeleteFixture(t)
	result, err := app.DeleteNodes([]DeletionTarget{targets["child"], targets["folder"], targets["folder-other"], targets["folder"]})
	if err != nil {
		t.Fatal(err)
	}
	if len(desktop.calls) != 2 || desktop.calls[0] != targets["folder"].Path || desktop.calls[1] != targets["folder-other"].Path {
		t.Fatalf("unexpected deletions: %v", desktop.calls)
	}
	if len(result.Deleted) != 2 || len(result.Failures) != 0 || result.FileCount != 1 || result.DirCount != 1 {
		t.Fatalf("batch accounting: %+v", result)
	}
}

func TestBatchDeletionValidatesEveryTargetBeforeStarting(t *testing.T) {
	for _, reason := range []string{"stale path", "protected", "changed mode", "root", "scan active", "disabled"} {
		t.Run(reason, func(t *testing.T) {
			app, desktop, targets := batchDeleteFixture(t)
			second := targets["last"]
			switch reason {
			case "stale path":
				second.Path += "-replaced"
			case "protected":
				desktop.blocked = second.Path
			case "changed mode":
				second.Action = "permanent"
			case "root":
				path, _ := app.store.NodePath(0)
				second = DeletionTarget{0, path, "trash"}
			case "scan active":
				app.scanActive = true
			case "disabled":
				app.profile.AllowDelete = false
			}
			if _, err := app.DeleteNodes([]DeletionTarget{targets["folder-other"], second}); err == nil {
				t.Fatal("invalid batch accepted")
			}
			if len(desktop.calls) != 0 {
				t.Fatal("validation failure allowed a partial deletion")
			}
		})
	}
}

func TestBatchDeletionContinuesAfterFailureAndRequiresRefresh(t *testing.T) {
	app, desktop, targets := batchDeleteFixture(t)
	desktop.fail = targets["folder-other"].Path
	result, err := app.DeleteNodes([]DeletionTarget{targets["folder-other"], targets["last"]})
	if err != nil {
		t.Fatal(err)
	}
	if len(desktop.calls) != 2 || len(result.Deleted) != 1 || len(result.Failures) != 1 || !result.RescanRequired {
		t.Fatalf("partial result: %+v, calls: %v", result, desktop.calls)
	}
	if result.Failures[0].Path != desktop.fail || result.FileCount != 2 {
		t.Fatalf("incorrect failure accounting: %+v", result)
	}
}

func TestBatchDeletionKeepsConfirmedModeWhenSettingsChange(t *testing.T) {
	app, desktop, targets := batchDeleteFixture(t)
	desktop.afterDelete = func() {
		profile := app.GetProfile()
		profile.AllowPermanentDelete = true
		if err := app.SetProfile(profile); err != nil {
			t.Fatal(err)
		}
	}
	result, err := app.DeleteNodes([]DeletionTarget{targets["folder-other"], targets["last"]})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Deleted) != 2 || desktop.permanentCalls != 0 {
		t.Fatalf("confirmed mode changed: %+v", result)
	}
}
