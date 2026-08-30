//go:build !windows

package main

import (
	"errors"
	"fmt"
	"path/filepath"
	"sync"

	"github.com/fsnotify/fsnotify"
)

type fsnotifyTreeWatcher struct {
	watcher *fsnotify.Watcher
	done    chan struct{}
	exited  chan struct{}
	once    sync.Once
	mu      sync.Mutex
	closed  bool
	paths   map[string]struct{}
}

func startTreeWatcher(_ string, directories []string, onChange func(string), onFailure func(error)) (treeWatcher, error) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	result := &fsnotifyTreeWatcher{
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

func (watcher *fsnotifyTreeWatcher) AddDirectory(directory string) error {
	clean := filepath.Clean(directory)
	watcher.mu.Lock()
	defer watcher.mu.Unlock()
	if watcher.closed {
		return errors.New("filesystem watcher is closed")
	}
	if _, exists := watcher.paths[clean]; exists {
		return nil
	}
	if err := watcher.watcher.Add(clean); err != nil {
		return err
	}
	watcher.paths[clean] = struct{}{}
	return nil
}

func (watcher *fsnotifyTreeWatcher) run(onChange func(string), onFailure func(error)) {
	defer close(watcher.exited)
	for {
		select {
		case event, ok := <-watcher.watcher.Events:
			if !ok {
				return
			}
			if event.Op&(fsnotify.Create|fsnotify.Write|fsnotify.Remove|fsnotify.Rename|fsnotify.Chmod) != 0 {
				onChange(filepath.Clean(event.Name))
			}
		case err, ok := <-watcher.watcher.Errors:
			if !ok {
				return
			}
			onFailure(err)
		case <-watcher.done:
			return
		}
	}
}

func (watcher *fsnotifyTreeWatcher) Close() error {
	var err error
	watcher.once.Do(func() {
		close(watcher.done)
		watcher.mu.Lock()
		watcher.closed = true
		err = watcher.watcher.Close()
		watcher.mu.Unlock()
		<-watcher.exited
	})
	return err
}
