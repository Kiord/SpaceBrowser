package main

import (
	"sort"
	"unsafe"
)

func (q *scanJobQueue) touchLocked(job *scanJob) {
	q.retentionClock++
	job.lastUsed = q.retentionClock
}

// Estimates retained Go objects, strings and backing arrays, not process RSS.
// Trees sharing their immutable root are charged once. Distinct copies are
// conservatively charged separately even when some strings remain shared.
func estimateSnapshotBytes(snapshot *treeStoreSnapshot) int64 {
	if snapshot == nil || snapshot.root == nil {
		return 0
	}
	total := int64(cap(snapshot.nodes)) * int64(unsafe.Sizeof((*Node)(nil)))
	var visit func(*Node)
	visit = func(node *Node) {
		if node == nil {
			return
		}
		total += int64(unsafe.Sizeof(Node{})) + int64(len(node.Name)+len(node.FullPath)) + int64(cap(node.Children))*int64(unsafe.Sizeof((*Node)(nil)))
		for _, child := range node.Children {
			visit(child)
		}
	}
	visit(snapshot.root)
	// Allocator size classes and per-object overhead are approximate.
	return total + total/4
}

// Called only at retention boundaries, never for every progress poll. q.mu
// serializes job metadata; scanner-owned trees are never modified or evicted.
func (q *scanJobQueue) pruneLocked() {
	if q.app == nil {
		return
	}
	budgetMiB := q.app.GetProfile().ScanMemoryBudgetMiB
	if budgetMiB == 0 {
		budgetMiB = 512
	}
	budget := int64(budgetMiB) << 20
	latest, completed := map[string]uint64{}, map[string]uint64{}
	for _, job := range q.jobs {
		key := canonicalCachePath(job.info.Path)
		if !pendingJob(job.info.State) {
			latest[key] = job.info.ID
		}
		if job.info.State == "completed" {
			completed[key] = job.info.ID
		}
	}
	kept := q.jobs[:0]
	for _, job := range q.jobs {
		key := canonicalCachePath(job.info.Path)
		pinned := job.worker != nil || job.info.ID == q.selected || job.info.ID == q.requested
		if !pinned && job.info.ID != latest[key] && job.info.ID != completed[key] {
			continue
		}
		// A newer successful rescan supersedes the old allocation, even under budget.
		if !pinned && job.info.State == "completed" && job.info.ID != completed[key] {
			continue
		}
		kept = append(kept, job)
	}
	clear(q.jobs[len(kept):])
	q.jobs = kept

	if q.retainedSizes == nil {
		q.retainedSizes = make(map[*Node]int64)
	}
	roots := make(map[*Node]int64)
	protected := make(map[*Node]bool)
	add := func(snapshot *treeStoreSnapshot, pinned bool) {
		if snapshot == nil || snapshot.root == nil {
			return
		}
		size, found := q.retainedSizes[snapshot.root]
		if !found {
			size = estimateSnapshotBytes(snapshot)
			q.retainedSizes[snapshot.root] = size
		}
		roots[snapshot.root] = size
		if pinned {
			protected[snapshot.root] = true
		}
	}
	add(q.app.store.sharedSnapshot(), true)
	for _, job := range q.jobs {
		pinned := job.info.ID == q.selected || job.info.ID == q.requested
		add(job.snapshot, pinned)
		add(job.previous, true)
		if job.worker != nil {
			add(job.worker.store.sharedSnapshot(), true)
		}
	}
	manager := q.app.scanCache
	var retired []*scanCacheEntry
	cacheOverhead := int64(0)

	if manager != nil {
		manager.mu.Lock()
		for _, entry := range manager.entries {
			add(&treeStoreSnapshot{root: entry.root, nodes: entry.nodes}, false)
			cacheOverhead += cacheIndexBytes(entry)
		}
	}
	total := cacheOverhead
	for _, size := range roots {
		total += size
	}
	// Evict cache-only roots first; displayed/rollback trees remain protected.
	type candidate struct {
		root      *Node
		used      uint64
		cacheOnly bool
	}
	candidates := make(map[*Node]candidate)
	for _, job := range q.jobs {
		if job.snapshot != nil && job.snapshot.root != nil && !protected[job.snapshot.root] {
			root := job.snapshot.root
			old, found := candidates[root]
			if !found || job.lastUsed > old.used {
				candidates[root] = candidate{root: root, used: job.lastUsed}
			}
		}
	}
	if manager != nil {
		for _, entry := range manager.entries {
			if !protected[entry.root] {
				if _, found := candidates[entry.root]; !found {
					candidates[entry.root] = candidate{root: entry.root, used: entry.lastUsed, cacheOnly: true}
				}
			}
		}
	}
	ordered := make([]candidate, 0, len(candidates))
	for _, entry := range candidates {
		ordered = append(ordered, entry)
	}
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].cacheOnly != ordered[j].cacheOnly {
			return ordered[i].cacheOnly
		}
		return ordered[i].used < ordered[j].used
	})
	for _, candidate := range ordered {
		if total <= budget {
			break
		}
		for _, job := range q.jobs {
			if job.snapshot != nil && job.snapshot.root == candidate.root {
				job.snapshot, job.nodeIDs = nil, nil
				job.info.Evicted = true
				job.revision++
			}
		}
		if manager != nil {
			for key, entry := range manager.entries {
				if entry.root == candidate.root {
					total -= cacheIndexBytes(entry)
					delete(manager.entries, key)
					retired = append(retired, entry)
				}
			}
		}
		total -= roots[candidate.root]
		delete(roots, candidate.root)
	}
	if manager != nil {
		manager.mu.Unlock()
	}
	for root := range q.retainedSizes {
		if _, exists := roots[root]; !exists {
			delete(q.retainedSizes, root)
		}
	}
	for _, entry := range retired {
		if entry.watcher != nil {
			_ = entry.watcher.Close()
		}
	}
}
