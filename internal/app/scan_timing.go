package app

import (
	"fmt"
	"io"
	"sync/atomic"
	"time"

	"spacebrowser/internal/platform"
)

type scanTimingStage uint8

const (
	scanTimingSetup scanTimingStage = iota
	scanTimingWalk
	scanTimingFinalize
	scanTimingCacheReuse
	scanTimingReadDirectory
	scanTimingEntries
	scanTimingMetadata
	scanTimingWatch
	scanTimingPreview
	scanTimingDirectoryOpen
	scanTimingDirectoryIdentity
	scanTimingDirectoryQuery
	scanTimingDirectoryDecode
	scanTimingDirectoryAllocation
	scanTimingDirectoryClose
	scanTimingDirectoryPortable
	scanTimingStageCount
)

var scanTimingLabels = [scanTimingStageCount]string{
	"Setup and cache lookup",
	"Directory walk and tree assembly",
	"Space accounting and finalization",
	"Complete-tree cache reuse",
	"Directory enumeration",
	"Directory entry processing",
	"Extra allocation / identity lookups",
	"Filesystem watcher registration",
	"Live preview snapshots",
	"Windows directory open API",
	"Windows directory identity APIs",
	"Windows directory enumeration API",
	"Windows directory record decoding (excludes allocation APIs)",
	"Windows compressed / sparse allocation API",
	"Windows directory close API",
	"Portable fallback directory read",
}

type scanTimingCounter struct {
	calls atomic.Int64
	nanos atomic.Int64
}

type scanTiming struct {
	stages             [scanTimingStageCount]scanTimingCounter
	directoryBreakdown atomic.Bool
	directoryReaders   [3]atomic.Int64
}

// Timers and counters are entirely skipped when the optional collector is nil.
func newScanTiming(enabled bool) *scanTiming {
	if !enabled {
		return nil
	}
	return &scanTiming{}
}

func (p *scanTiming) start() time.Time {
	if p == nil {
		return time.Time{}
	}
	return time.Now()
}

func (p *scanTiming) finish(stage scanTimingStage, started time.Time) {
	if p == nil {
		return
	}
	p.record(stage, time.Since(started))
}

func (p *scanTiming) record(stage scanTimingStage, duration time.Duration) {
	if p == nil {
		return
	}
	p.stages[stage].nanos.Add(int64(duration))
	p.stages[stage].calls.Add(1)
}

func (p *scanTiming) recordDirectoryRead(timing *platform.DirectoryReadTimings) {
	if p == nil || timing == nil {
		return
	}
	p.directoryBreakdown.Store(true)
	p.directoryReaders[0].Add(timing.ExtendedAttempts)
	p.directoryReaders[1].Add(timing.LegacyAttempts)
	p.directoryReaders[2].Add(timing.PortableAttempts)
	metrics := [...]platform.DirectoryReadMetric{timing.Open, timing.Identity, timing.Query, timing.Decode, timing.Allocation, timing.Close, timing.Portable}
	for index, metric := range metrics {
		counter := &p.stages[int(scanTimingDirectoryOpen)+index]
		counter.calls.Add(metric.Calls)
		counter.nanos.Add(int64(metric.Duration))
	}
}

type ScanTimingMetric struct {
	Name     string
	Calls    int64
	Duration time.Duration
}

type ScanTimingSnapshot struct {
	Active            time.Duration
	Workers           int
	ReusedDirectories int64
	Phases            []ScanTimingMetric
	Operations        []ScanTimingMetric
	DirectoryReaders  *ScanDirectoryReaders
}

type ScanDirectoryReaders struct {
	Extended, Legacy, Portable int64
}

func (p *scanTiming) snapshot(active time.Duration, workers int, reused int64) *ScanTimingSnapshot {
	if p == nil {
		return nil
	}
	result := &ScanTimingSnapshot{Active: active, Workers: workers, ReusedDirectories: reused}
	if p.directoryBreakdown.Load() {
		result.DirectoryReaders = &ScanDirectoryReaders{p.directoryReaders[0].Load(), p.directoryReaders[1].Load(), p.directoryReaders[2].Load()}
	}
	for stage := scanTimingStage(0); stage < scanTimingStageCount; stage++ {
		counter := &p.stages[stage]
		if calls := counter.calls.Load(); calls > 0 {
			metric := ScanTimingMetric{Name: scanTimingLabels[stage], Calls: calls, Duration: time.Duration(counter.nanos.Load())}
			if stage < scanTimingReadDirectory {
				result.Phases = append(result.Phases, metric)
			} else {
				result.Operations = append(result.Operations, metric)
			}
		}
	}
	return result
}

func (p *scanTiming) snapshotForScan(pause *scanPause, started time.Time, wall time.Duration, workers int, reused int64) *ScanTimingSnapshot {
	if p == nil {
		return nil
	}
	return p.snapshot(pause.elapsed(started, started.Add(wall)), workers, reused)
}

func writeScanTiming(output io.Writer, timing *ScanTimingSnapshot, wall time.Duration) {
	if timing == nil {
		return
	}
	fmt.Fprintln(output, "\nTiming profile")
	fmt.Fprintf(output, "Elapsed before report writing: %s\nActive scan time: %s\nPaused time: %s\n", wall.Round(time.Microsecond), timing.Active.Round(time.Microsecond), max(0, wall-timing.Active).Round(time.Microsecond))
	fmt.Fprintf(output, "Scanner worker limit: %d\nCached directories reused: %d\n", timing.Workers, timing.ReusedDirectories)
	if readers := timing.DirectoryReaders; readers != nil {
		fmt.Fprintln(output, "Directory reader attempts (includes failed reads and retries):")
		fmt.Fprintf(output, "  - Native Windows 128-bit: %d\n  - Native Windows 64-bit: %d\n  - Portable fallback: %d\n", readers.Extended, readers.Legacy, readers.Portable)
	}
	fmt.Fprintln(output, "Elapsed scan phases:")
	for _, metric := range timing.Phases {
		fmt.Fprintf(output, "  - %s: %s (%d calls)\n", metric.Name, metric.Duration.Round(time.Microsecond), metric.Calls)
	}
	fmt.Fprintln(output, "Cumulative operation timings:")
	for _, metric := range timing.Operations {
		fmt.Fprintf(output, "  - %s: %s (%d calls, average %s)\n", metric.Name, metric.Duration.Round(time.Microsecond), metric.Calls, (metric.Duration / time.Duration(metric.Calls)).Round(time.Microsecond))
	}
	fmt.Fprintln(output, "Concurrent and nested timings overlap; do not add them or interpret them as CPU time. Operation timings include I/O, lock waits and any pauses during those operations.")
	if timing.DirectoryReaders != nil {
		fmt.Fprintln(output, "Windows API timings measure elapsed time inside calls, including kernel work, filesystem filters and waiting; they do not isolate physical disk latency.")
	}
	fmt.Fprintln(output, "Report writing, saved-snapshot serialization and frontend rendering are outside this profile. Timing collection itself adds overhead.")
}
