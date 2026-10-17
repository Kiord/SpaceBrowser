package main

import (
	"unsafe"
)

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

// Release superseded scans of the same location, never the latest completed
// tree of another location. Cache limits must not discard browsable results.
func (q *scanJobQueue) pruneLocked() {
	if q.app == nil {
		return
	}
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

}
