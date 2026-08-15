package main

import (
	"fmt"
	"sync"
	"sync/atomic"
)

const maximumScanReportExamples = 5

type scanSkipReason uint8

const (
	scanSkipExcluded scanSkipReason = iota
	scanSkipHidden
	scanSkipSymlink
	scanSkipNetwork
	scanSkipNonRegular
	scanSkipDuplicateIdentity
	scanSkipRepeatedDirectory
	scanSkipReasonCount
)

var scanSkipLabels = [scanSkipReasonCount]string{
	"excluded",
	"hidden",
	"symlinks",
	"network filesystems",
	"non-regular files",
	"deduplicated hard-link paths",
	"repeated directories",
}

type scanErrorReason uint8

const (
	scanErrorReadDirectory scanErrorReason = iota
	scanErrorSymlinkTarget
	scanErrorFileMetadata
	scanErrorDirectoryMetadata
	scanErrorUsageMetadata
	scanErrorResolveSymlink
	scanErrorSubdirectory
	scanErrorReasonCount
)

var scanErrorLabels = [scanErrorReasonCount]string{
	"unreadable directories",
	"symlink target metadata",
	"file metadata",
	"directory metadata",
	"allocation or identity metadata",
	"symlink resolution",
	"subdirectory scans",
}

type ScanReportExample struct {
	Reason string
	Path   string
	Error  string
}

type ScanReportSnapshot struct {
	Skipped  [scanSkipReasonCount]int64
	Errors   [scanErrorReasonCount]int64
	Examples []ScanReportExample
}

func (r ScanReportSnapshot) TotalSkipped() int64 {
	var total int64
	for _, count := range r.Skipped {
		total += count
	}
	return total
}

func (r ScanReportSnapshot) TotalErrors() int64 {
	var total int64
	for _, count := range r.Errors {
		total += count
	}
	return total
}

type ScanReport struct {
	skipped [scanSkipReasonCount]atomic.Int64
	errors  [scanErrorReasonCount]atomic.Int64

	examplesMu   sync.Mutex
	examples     []ScanReportExample
	exampleLimit int
}

func NewScanReport(exampleLimit int) ScanReport {
	return ScanReport{exampleLimit: exampleLimit}
}

func (r *ScanReport) SetExampleLimit(limit int) {
	r.examplesMu.Lock()
	r.exampleLimit = limit
	r.examplesMu.Unlock()
}

func (r *ScanReport) RecordSkip(reason scanSkipReason) {
	if r == nil || reason >= scanSkipReasonCount {
		return
	}
	r.skipped[reason].Add(1)
}

func (r *ScanReport) RecordError(reason scanErrorReason, path string, err error) {
	if r == nil || reason >= scanErrorReasonCount {
		return
	}
	r.errors[reason].Add(1)

	r.examplesMu.Lock()
	defer r.examplesMu.Unlock()
	if r.exampleLimit >= 0 && len(r.examples) >= r.exampleLimit {
		return
	}
	example := ScanReportExample{Reason: scanErrorLabels[reason], Path: path}
	if err != nil {
		example.Error = err.Error()
	}
	r.examples = append(r.examples, example)
}

func (r *ScanReport) Snapshot() ScanReportSnapshot {
	var snapshot ScanReportSnapshot
	if r == nil {
		return snapshot
	}
	for reason := scanSkipReason(0); reason < scanSkipReasonCount; reason++ {
		snapshot.Skipped[reason] = r.skipped[reason].Load()
	}
	for reason := scanErrorReason(0); reason < scanErrorReasonCount; reason++ {
		snapshot.Errors[reason] = r.errors[reason].Load()
	}
	r.examplesMu.Lock()
	snapshot.Examples = append([]ScanReportExample(nil), r.examples...)
	r.examplesMu.Unlock()
	return snapshot
}

func formatNonzeroScanCounts(counts []int64, labels []string) string {
	result := ""
	for index, count := range counts {
		if count == 0 {
			continue
		}
		if result != "" {
			result += ", "
		}
		result += fmt.Sprintf("%s=%d", labels[index], count)
	}
	return result
}
