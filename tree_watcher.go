package main

import "spacebrowser/internal/treewatch"

var errTreeWatchCapacity = treewatch.ErrCapacity

type treeWatcher = treewatch.Watcher

func startTreeWatcher(root string, directories []string, onChange func(string), onSubtree func(string), onFailure func(error)) (treeWatcher, error) {
	return treewatch.Start(root, directories, onChange, onSubtree, onFailure)
}
