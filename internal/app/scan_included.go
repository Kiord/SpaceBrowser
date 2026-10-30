package app

// updateIncludedScansLocked publishes independent subtree snapshots only after a
// successful full scan, also merging narrower scans into saved ancestors.
// Pending jobs keep ownership of their own results, and
// folders absent from the new tree (including excluded folders) stay unchanged.
func (q *scanJobQueue) updateIncludedScansLocked(source *scanJob) []*scanJob {
	var updated []*scanJob
	var directories map[string]*Node
	for _, job := range q.jobs {
		if job == source || job.info.State != "completed" ||
			(job.info.ID != 0 && job.info.ID == source.info.ID) ||
			(!source.info.Partial && canonicalCachePath(job.info.Path) == canonicalCachePath(source.info.Path)) {
			continue
		}
		switch {
		case cachePathWithin(job.info.Path, source.info.Path):
			if directories == nil {
				directories = indexCachedDirectories(source.snapshot.root)
			}
			root := directories[canonicalCachePath(job.info.Path)]
			if root == nil {
				continue
			}
			job.snapshot, job.nodeIDs = includedScanSnapshot(root, job.snapshot)
		case cachePathWithin(source.info.Path, job.info.Path):
			previous := job.snapshot
			if previous == nil && job.info.Cached {
				if q.app == nil || q.app.coldCache == nil {
					continue
				}
				var err error
				previous, err = q.app.coldCache.load(job.info.Path)
				if err != nil {
					q.app.logger.Warningf("could not update saved ancestor scan: %v", err)
					continue
				}
			}
			snapshot, ids := mergeIncludedScan(previous, source.snapshot.root)
			if snapshot == nil {
				continue
			}
			job.snapshot, job.nodeIDs = snapshot, ids
		default:
			continue
		}
		job.info.ScannedAt = source.info.ScannedAt
		// A volume report includes unrelated paths; don't attribute it to a folder.
		job.info.Report = nil
		job.info.Error, job.info.CacheError = "", ""
		// Updating a region does not measure a new scan of this whole location.
		// Retain the duration of its original scan when saving the updated tree.
		job.info.Progress = ScanProgress{Fraction: 1, FileCount: int64(job.snapshot.files), DirCount: int64(job.snapshot.dirs),
			ElapsedMilliseconds: job.info.Progress.ElapsedMilliseconds}
		job.revision++
		if q.selected == job.info.ID {
			// Prevent navigation from saving the old foreground tree over this update.
			q.viewKey = ""
		}
		updated = append(updated, job)
	}
	return updated
}

// Publish only the refreshed regions of a partial scan. Its other branches are
// historical snapshots and must not overwrite more recent overlapping scans.
func (q *scanJobQueue) updateRefreshedScansLocked(source *scanJob, paths []string) []*scanJob {
	directories := indexCachedDirectories(source.snapshot.root)
	seen := make(map[*scanJob]bool)
	var updated []*scanJob
	for _, path := range paths {
		root := directories[canonicalCachePath(path)]
		if root == nil {
			continue
		}
		region := &scanJob{info: source.info, snapshot: &treeStoreSnapshot{root: root}}
		region.info.Path = path
		for _, job := range q.updateIncludedScansLocked(region) {
			if !seen[job] {
				seen[job] = true
				updated = append(updated, job)
			}
		}
	}
	return updated
}

func setScanDate(root *Node, date int64) {
	if root == nil {
		return
	}
	root.ScanTime = date
	for _, child := range root.Children {
		setScanDate(child, date)
	}
}

// Keep IDs for surviving paths so saved navigation remains valid across repeated
// background updates. Removed IDs are never reused. No mutable nodes are shared.
func includedScanSnapshot(root *Node, previous *treeStoreSnapshot) (*treeStoreSnapshot, map[int]int) {
	return scanSnapshotWithIDs(root, previous, false)
}

func mergeIncludedScan(previous *treeStoreSnapshot, root *Node) (*treeStoreSnapshot, map[int]int) {
	if previous == nil || previous.root == nil {
		return nil, nil
	}
	target := indexCachedDirectories(previous.root)[canonicalCachePath(root.FullPath)]
	if target == nil {
		return nil, nil
	}
	var store TreeStore
	store.restoreState(previous)
	// Detach before replacing: snapshots restored from older callers may not yet
	// be marked shared even though this update must leave them immutable.
	store.shared = true
	files, dirs := subtreeEntryCounts(root)
	if _, err := store.ReplaceSubtree(target.ID, root, files, dirs); err != nil {
		return nil, nil
	}
	return scanSnapshotWithIDs(store.root, previous, true)
}

func scanSnapshotWithIDs(root *Node, previous *treeStoreSnapshot, keepFreeSpace bool) (*treeStoreSnapshot, map[int]int) {
	snapshot := &treeStoreSnapshot{shared: true}
	if keepFreeSpace && previous != nil {
		snapshot.hasDiskUsage, snapshot.diskTotal, snapshot.diskFree = previous.hasDiskUsage, previous.diskTotal, previous.diskFree
	}
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
			if keepFreeSpace || !child.IsFreeSpace {
				node.Children = append(node.Children, clone(child, node.ID, depth+1))
			}
		}
		return &node
	}
	snapshot.root = clone(root, -1, 0)
	snapshot.files, snapshot.dirs = subtreeEntryCounts(snapshot.root)
	return snapshot, ids
}
