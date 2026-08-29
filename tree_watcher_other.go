//go:build !windows

package main

import (
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
}

func startTreeWatcher(_ string, directories []string, onChange func(string), onFailure func(error)) (treeWatcher, error) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	for _, directory := range directories {
		if err := watcher.Add(directory); err != nil {
			watcher.Close()
			return nil, fmt.Errorf("watch %s: %w", directory, err)
		}
	}
	result := &fsnotifyTreeWatcher{watcher: watcher, done: make(chan struct{}), exited: make(chan struct{})}
	go result.run(onChange, onFailure)
	return result, nil
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
		err = watcher.watcher.Close()
		<-watcher.exited
	})
	return err
}
