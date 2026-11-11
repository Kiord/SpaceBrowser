package app

import (
	"context"
	"errors"
	"fmt"

	"spacebrowser/internal/platform"
)

const scanMethodNTFS = platform.FastScanNTFSLayout
const scanMethodNTFSDirect = platform.FastScanNTFSDirect
const scanMethodDirectory = "Directory enumeration"

// Version the visible accounting rules independently of saved historical views.
// Bump when old live trees must not accelerate a scan using the current reader.
const ntfsReaderVersion = 2

func isNTFSScanMethod(name string) bool {
	return name == scanMethodNTFS || name == scanMethodNTFSDirect
}

func isNTFSScanReport(method *ScanMethodInfo) bool {
	return method != nil && (isNTFSScanMethod(method.Name) || isNTFSScanMethod(method.SourceName))
}

func (s *Scanner) prepareFastScan(path string) error {
	provider, ok := s.filesystem.(platform.FastScanProvider)
	if !ok {
		return nil
	}
	method := &ScanMethodInfo{Name: scanMethodDirectory}
	if !s.profile.UseMFT {
		method.FallbackReason = "Fast NTFS scanning is disabled in settings"
	} else if s.profile.FollowSymlinks {
		method.FallbackReason = "Following symlinks requires directory enumeration"
	} else {
		started := s.timing.start()
		indexed, stats, err := provider.PrepareFastScan(s.ctx, path, func() error { return s.pause.wait(s.ctx) }, platform.FastScanOptions{Timing: s.timing != nil})
		s.timing.finish(scanTimingNTFS, started)
		s.timing.recordFastScan(stats.Timing)
		method.Records, method.Batches, method.Bytes = stats.Records, stats.Batches, stats.Bytes
		method.DirectFallbackReason = stats.DirectFallbackReason
		method.DirectRecords, method.DirectBatches, method.DirectBytes = stats.DirectRecords, stats.DirectBatches, stats.DirectBytes
		method.DuplicateNames, method.ReconciledDirectories, method.ReconciledSubtrees = stats.DuplicateNames, stats.ReconciledDirectories, stats.ReconciledSubtrees
		if err := s.ctx.Err(); err != nil {
			return err
		}
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return err
		}
		if err == nil && indexed == nil {
			err = fmt.Errorf("NTFS provider returned no metadata index")
		}
		if err != nil {
			method.FallbackReason = err.Error()
		} else {
			method.Name = scanMethodNTFS
			method.ReaderVersion = ntfsReaderVersion
			if stats.Method == platform.FastScanNTFSDirect {
				method.Name = scanMethodNTFSDirect
			}
			s.filesystem = indexed
			// Indexed and directory scans can see different permission scopes.
			// Rebuild from one source rather than mixing indexed and old subtrees.
			s.SetIncrementalCache(nil, nil)
			s.SetIncrementalCacheReports(nil)
		}
	}
	s.method.Store(method)
	return nil
}
