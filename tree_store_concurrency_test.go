package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type storeOperation func(*TreeStore, func(string) bool, func(string) error) (DeleteResult, error)

var storeOperations = map[string]storeOperation{
	"trash": func(s *TreeStore, predicate func(string) bool, action func(string) error) (DeleteResult, error) {
		return s.DeleteNode(1, predicate, nil, action)
	},
	"permanent": func(s *TreeStore, predicate func(string) bool, action func(string) error) (DeleteResult, error) {
		return s.DeleteNodePermanently(1, predicate, nil, action)
	},
	"empty-trash": func(s *TreeStore, predicate func(string) bool, action func(string) error) (DeleteResult, error) {
		return s.EmptyTrashNode(1, predicate, action)
	},
}

func concurrencyStore(t *testing.T) *TreeStore {
	t.Helper()
	path := filepath.Join(t.TempDir(), "Trash")
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	root := &Node{ID: 0, ParentID: -1, IsFolder: true, Size: 10}
	folder := &Node{ID: 1, ParentID: 0, Name: "Trash", FullPath: path, IsFolder: true, Size: 10}
	file := &Node{ID: 2, ParentID: 1, Name: "file", Size: 10}
	root.Children, folder.Children = []*Node{folder}, []*Node{file}
	store := &TreeStore{}
	store.Replace(root, []*Node{root, folder, file}, 1, 2)
	return store
}

func awaitStoreSignal(t *testing.T, signal <-chan struct{}) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(2 * time.Second):
		t.Fatal("store operation blocked unexpectedly")
	}
}

func TestFilesystemOperationsLeaveTreeReadable(t *testing.T) {
	for name, operation := range storeOperations {
		t.Run(name, func(t *testing.T) {
			store := concurrencyStore(t)
			entered, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
			t.Cleanup(func() { close(release); awaitStoreSignal(t, done) })
			go func() {
				defer close(done)
				_, err := operation(store, func(string) bool { return name == "empty-trash" }, func(string) error {
					close(entered)
					<-release
					return nil
				})
				if err != nil {
					t.Error(err)
				}
			}()
			awaitStoreSignal(t, entered)
			readDone := make(chan struct{})
			go func() {
				defer close(readDone)
				if _, err := store.Layout(0, 800, 600, 1, false); err != nil {
					t.Error(err)
				}
				if _, err := store.NodePath(1); err != nil {
					t.Error(err)
				}
				if files, dirs := store.Counts(); files != 1 || dirs != 2 {
					t.Errorf("premature mutation: %d/%d", files, dirs)
				}
			}()
			awaitStoreSignal(t, readDone)
		})
	}
}

func TestFilesystemCompletionCannotMutateReplacementTree(t *testing.T) {
	for name, operation := range storeOperations {
		for _, shared := range []bool{false, true} {
			t.Run(name+map[bool]string{false: "/owned", true: "/shared"}[shared], func(t *testing.T) {
				store := concurrencyStore(t)
				result, err := operation(store, func(string) bool { return name == "empty-trash" }, func(string) error {
					// Reuse exactly the same IDs in the new tree. Completion must
					// not delete these unrelated nodes or overwrite their counts.
					root := &Node{ID: 0, ParentID: -1, IsFolder: true, Size: 99}
					child := &Node{ID: 1, ParentID: 0, Name: "replacement", Size: 99}
					root.Children = []*Node{child}
					if shared {
						store.ReplaceShared(root, []*Node{root, child}, 1, 1)
					} else {
						store.Replace(root, []*Node{root, child}, 1, 1)
					}
					return nil
				})
				if err != nil {
					t.Fatal(err)
				}
				if !result.RescanRequired || result.FileCount != 1 || result.DirCount != 1 || len(result.trashRefreshes) != 0 {
					t.Fatalf("stale completion = %+v", result)
				}
				if store.root.Size != 99 || store.nodes[1].Name != "replacement" {
					t.Fatal("replacement tree was mutated")
				}
			})
		}
	}
}

func TestFilesystemFailureLeavesTreeUnchanged(t *testing.T) {
	for name, operation := range storeOperations {
		t.Run(name, func(t *testing.T) {
			store := concurrencyStore(t)
			failure := errors.New("native operation failed")
			_, err := operation(store, func(string) bool { return name == "empty-trash" }, func(string) error { return failure })
			if !errors.Is(err, failure) {
				t.Fatalf("error = %v", err)
			}
			if store.root.Size != 10 || store.nodes[1] == nil || store.nodes[2] == nil {
				t.Fatal("failed action changed tree")
			}
		})
	}
}

func TestNativePredicatesRunOutsideTreeLock(t *testing.T) {
	store := concurrencyStore(t)
	done := make(chan struct{})
	go func() {
		defer close(done)
		store.NodePathMatches(1, func(string) bool { store.UpdateDiskUsage(100, 50); return false })
		_, err := store.DeleteNode(1, func(string) bool {
			// A write would deadlock even under a read lock.
			store.UpdateDiskUsage(100, 50)
			return false
		}, nil, func(string) error { return nil })
		if err != nil {
			t.Error(err)
		}
	}()
	awaitStoreSignal(t, done)
}

func TestFilesystemMutationRevalidatesAfterNativeChecks(t *testing.T) {
	store := concurrencyStore(t)
	called := false
	_, err := store.DeleteNode(1, func(string) bool {
		store.Replace(nil, nil, 0, 0)
		return false
	}, nil, func(string) error { called = true; return nil })
	if err == nil || called {
		t.Fatalf("changed selection reached native action: called=%t err=%v", called, err)
	}
}

func TestFilesystemMutationsAreSerialized(t *testing.T) {
	store := concurrencyStore(t)
	entered, release, firstDone := make(chan struct{}), make(chan struct{}), make(chan struct{})
	secondStarted, secondEntered, secondDone := make(chan struct{}), make(chan struct{}), make(chan struct{})
	t.Cleanup(func() { close(release); awaitStoreSignal(t, firstDone); awaitStoreSignal(t, secondDone) })
	go func() {
		defer close(firstDone)
		_, _ = store.DeleteNode(1, nil, nil, func(string) error {
			close(entered)
			<-release
			return errors.New("keep the target for the next operation")
		})
	}()
	awaitStoreSignal(t, entered)
	go func() {
		defer close(secondDone)
		close(secondStarted)
		_, err := store.EmptyTrashNode(1, func(string) bool { return true }, func(string) error {
			close(secondEntered)
			return nil
		})
		if err != nil {
			t.Error(err)
		}
	}()
	awaitStoreSignal(t, secondStarted)
	select {
	case <-secondEntered:
		t.Fatal("native mutations overlapped")
	case <-time.After(50 * time.Millisecond):
	}
}

func TestFilesystemCompletionCannotOverwriteRefreshedSubtree(t *testing.T) {
	store := concurrencyStore(t)
	path, _ := store.NodePath(1)
	result, err := store.DeleteNode(1, nil, nil, func(string) error {
		_, err := store.ReplaceSubtree(1, &Node{FullPath: path, IsFolder: true, Size: 42}, 0, 1)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.RescanRequired || store.nodes[1] == nil || store.nodes[1].Size != 42 {
		t.Fatalf("stale completion overwrote refreshed subtree: %+v", result)
	}
}
