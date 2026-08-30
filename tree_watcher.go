package main

type treeWatcher interface {
	AddDirectory(string) error
	Close() error
}
