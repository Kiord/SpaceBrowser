package treewatch

import "errors"

var ErrCapacity = errors.New("filesystem watch capacity reached")

type Watcher interface {
	AddDirectory(string) error
	Close() error
}
