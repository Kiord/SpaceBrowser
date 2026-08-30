//go:build !windows && (!darwin || !cgo)

package treewatch

import (
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"syscall"

	"github.com/fsnotify/fsnotify"
)

const maximumFSNotifyTreeWatches = 4096

type fsnotifyWatcher struct {
	watcher *fsnotify.Watcher
	done    chan struct{}
	exited  chan struct{}
	once    sync.Once
	mu      sync.Mutex
	closed  bool
	paths   map[string]struct{}
}

func Start(_ string, directories []string, onChange func(string), _ func(string), onFailure func(error)) (Watcher, error) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	result := &fsnotifyWatcher{
		watcher: watcher, done: make(chan struct{}), exited: make(chan struct{}),
		paths: make(map[string]struct{}),
	}
	for _, directory := range directories {
		if err := result.AddDirectory(directory); err != nil {
			watcher.Close()
			return nil, fmt.Errorf("watch %s: %w", directory, err)
		}
	}
	go result.run(onChange, onFailure)
	return result, nil
}

func (watcher *fsnotifyWatcher) AddDirectory(directory string) error {
	clean := filepath.Clean(directory)
	watcher.mu.Lock()
	defer watcher.mu.Unlock()
	if watcher.closed {
		return errors.New("filesystem watcher is closed")
	}
	if _, exists := watcher.paths[clean]; exists {
		return nil
	}
	if len(watcher.paths) >= maximumFSNotifyTreeWatches {
		return fmt.Errorf("%w (%d directories)", ErrCapacity, maximumFSNotifyTreeWatches)
	}
	if err := watcher.watcher.Add(clean); err != nil {
		if errors.Is(err, syscall.ENOSPC) || errors.Is(err, syscall.EMFILE) || errors.Is(err, syscall.ENFILE) || errors.Is(err, syscall.ENOMEM) {
			return fmt.Errorf("%w: %v", ErrCapacity, err)
		}
		return err
	}
	watcher.paths[clean] = struct{}{}
	return nil
}

func (watcher *fsnotifyWatcher) run(onChange func(string), onFailure func(error)) {
	defer close(watcher.exited)
	for {
		select {
		case event, ok := <-watcher.watcher.Events:
			if !ok {
				if !watcher.isClosed() {
					onFailure(errors.New("filesystem watcher event stream closed"))
				}
				return
			}
			if event.Op&(fsnotify.Create|fsnotify.Write|fsnotify.Remove|fsnotify.Rename|fsnotify.Chmod) != 0 {
				onChange(filepath.Clean(event.Name))
			}
		case err, ok := <-watcher.watcher.Errors:
			if !ok {
				if !watcher.isClosed() {
					onFailure(errors.New("filesystem watcher error stream closed"))
				}
				return
			}
			onFailure(err)
			return
		case <-watcher.done:
			return
		}
	}
}

func (watcher *fsnotifyWatcher) isClosed() bool {
	watcher.mu.Lock()
	defer watcher.mu.Unlock()
	return watcher.closed
}

func (watcher *fsnotifyWatcher) Close() error {
	var err error
	watcher.once.Do(func() {
		watcher.mu.Lock()
		watcher.closed = true
		watcher.mu.Unlock()
		close(watcher.done)
		err = watcher.watcher.Close()
		<-watcher.exited
	})
	return err
}
