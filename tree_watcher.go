package main

import "errors"

var errTreeWatchCapacity = errors.New("filesystem watch capacity reached")

type treeWatcher interface {
	AddDirectory(string) error
	Close() error
}
