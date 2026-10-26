package app

// updateIncludedScansLocked publishes independent subtree snapshots only after a
// successful full scan. Pending jobs keep ownership of their own results, and
// folders absent from the new tree (including excluded folders) stay unchanged.
func (q *scanJobQueue) updateIncludedScansLocked(source *scanJob) []*scanJob {
	var updated []*scanJob
	var directories map[string]*Node
	for _, job := range q.jobs {
		if job == source || job.info.State != "completed" ||
			canonicalCachePath(job.info.Path) == canonicalCachePath(source.info.Path) ||
			!cachePathWithin(job.info.Path, source.info.Path) {
			continue
		}
		if directories == nil {
			directories = indexCachedDirectories(source.snapshot.root)
		}
		root := directories[canonicalCachePath(job.info.Path)]
		if root == nil {
			continue
		}
		job.snapshot, job.nodeIDs = includedScanSnapshot(root, job.snapshot)
		job.info.ScannedAt = source.info.ScannedAt
		// A volume report includes unrelated paths; don't attribute it to a folder.
		job.info.Report = nil
		job.info.Error, job.info.CacheError = "", ""
		job.info.Progress = ScanProgress{Fraction: 1, FileCount: int64(job.snapshot.files), DirCount: int64(job.snapshot.dirs)}
		job.revision++
		if q.selected == job.info.ID {
			// Prevent navigation from saving the old foreground tree over this update.
			q.viewKey = ""
		}
		updated = append(updated, job)
	}
	return updated
}

// Keep IDs for surviving paths so saved navigation remains valid across repeated
// background updates. Removed IDs are never reused. No mutable nodes are shared.
func includedScanSnapshot(root *Node, previous *treeStoreSnapshot) (*treeStoreSnapshot, map[int]int) {
	snapshot := &treeStoreSnapshot{shared: true}
	previousIDs := make(map[string]int)
	ids := make(map[int]int)
	if previous != nil {
		snapshot.nodes = make([]*Node, len(previous.nodes))
		for id, node := range previous.nodes {
			ids[id] = -1
			if node != nil {
				previousIDs[canonicalCachePath(node.FullPath)] = node.ID
			}
		}
	}
	var clone func(*Node, int, int) *Node
	clone = func(source *Node, parent, depth int) *Node {
		node := *source
		node.ParentID, node.Depth = parent, depth
		node.Children = make([]*Node, 0, len(source.Children))
		if node.ID >= 0 {
			if id, exists := previousIDs[canonicalCachePath(node.FullPath)]; exists {
				node.ID = id
				snapshot.nodes[id] = &node
				ids[id] = id
			} else {
				node.ID = len(snapshot.nodes)
				snapshot.nodes = append(snapshot.nodes, &node)
			}
		}
		for _, child := range source.Children {
			if !child.IsFreeSpace {
				node.Children = append(node.Children, clone(child, node.ID, depth+1))
			}
		}
		return &node
	}
	snapshot.root = clone(root, -1, 0)
	snapshot.files, snapshot.dirs = subtreeEntryCounts(snapshot.root)
	return snapshot, ids
}
