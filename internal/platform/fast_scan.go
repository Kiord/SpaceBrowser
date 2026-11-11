package platform

import (
	"context"
	"time"
)

// FastScanProvider optionally supplies a read-only filesystem backed by a bulk
// metadata index. Callers keep their ordinary filesystem for desktop operations.
// Failure leaves that filesystem usable; cancellation must not trigger fallback.
type FastScanProvider interface {
	PrepareFastScan(context.Context, string, func() error, FastScanOptions) (ScannerFilesystem, FastScanStats, error)
}

type FastScanOptions struct{ Timing bool }

const (
	FastScanNTFSLayout = "NTFS file table (FSCTL_QUERY_FILE_LAYOUT)"
	FastScanNTFSDirect = "NTFS file table (direct MFT reads)"
)

type FastScanStats struct {
	Method                string
	DirectFallbackReason  string
	DirectRecords         int64
	DirectBatches         int64
	DirectBytes           int64
	Records               int64
	Batches               int64
	Bytes                 int64
	DuplicateNames        int64
	ReconciledDirectories int64
	ReconciledSubtrees    int64
	Timing                *FastScanTimings
}

type FastScanMetric struct {
	Calls    int64
	Duration time.Duration
}
type FastScanTimings struct{ Query, Decode, Records, Hierarchy, Wait, DirectRead, DirectMap, DirectTableRead, DirectListRead FastScanMetric }

type fastScanTimingStage uint8

const (
	fastScanQuery fastScanTimingStage = iota
	fastScanDecode
	fastScanRecords
	fastScanHierarchy
	fastScanWait
	fastScanDirectRead
	fastScanDirectMap
	fastScanDirectTableRead
	fastScanDirectListRead
)

func (p *FastScanTimings) start() time.Time {
	if p == nil {
		return time.Time{}
	}
	return time.Now()
}

func (p *FastScanTimings) finish(stage fastScanTimingStage, started time.Time) {
	if p == nil {
		return
	}
	p.record(stage, time.Since(started))
}

func (p *FastScanTimings) record(stage fastScanTimingStage, duration time.Duration) {
	if p == nil {
		return
	}
	metrics := [...]*FastScanMetric{&p.Query, &p.Decode, &p.Records, &p.Hierarchy, &p.Wait, &p.DirectRead, &p.DirectMap, &p.DirectTableRead, &p.DirectListRead}
	metric := metrics[stage]
	metric.Calls++
	metric.Duration += duration
}
