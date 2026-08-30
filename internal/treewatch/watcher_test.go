package treewatch

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWatcherReportsNestedChanges(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "nested")
	if err := os.Mkdir(nested, 0o700); err != nil {
		t.Fatal(err)
	}
	changes := make(chan string, 16)
	failures := make(chan error, 1)
	reportChange := func(path string) {
		select {
		case changes <- path:
		default:
		}
	}
	watcher, err := Start(root, []string{root, nested}, reportChange, reportChange, func(err error) {
		select {
		case failures <- err:
		default:
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	defer watcher.Close()

	deadline := time.After(5 * time.Second)
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for attempt := 0; ; attempt++ {
		select {
		case changed := <-changes:
			if filepath.Clean(filepath.Dir(changed)) == filepath.Clean(nested) {
				return
			}
		case watcherErr := <-failures:
			t.Fatalf("watcher failed: %v", watcherErr)
		case <-ticker.C:
			path := filepath.Join(nested, fmt.Sprintf("change-%d.tmp", attempt))
			if err := os.WriteFile(path, []byte("changed"), 0o600); err != nil {
				t.Fatal(err)
			}
		case <-deadline:
			t.Fatal("nested filesystem change was not reported")
		}
	}
}
