package platform

import "time"

type DirectoryReadMetric struct {
	Calls    int64
	Duration time.Duration
}

// Each collector belongs to one synchronous directory read, including any
// retries. The scanner aggregates it after the read, outside the Windows calls.
type DirectoryReadTimings struct {
	Open, Identity, Query, Decode, Allocation, Close, Portable DirectoryReadMetric
	ExtendedAttempts, LegacyAttempts, PortableAttempts         int64
}

func (p *DirectoryReadTimings) start() time.Time {
	if p == nil {
		return time.Time{}
	}
	return time.Now()
}

func (p *DirectoryReadTimings) finish(metric *DirectoryReadMetric, started time.Time) {
	metric.Calls++
	metric.Duration += time.Since(started)
}

type timedDirectoryReader interface {
	ReadDirWithTimings(string) ([]DirectoryEntry, *DirectoryReadDiagnostic, *DirectoryReadTimings, error)
}

func ReadDirWithTimings(filesystem ScannerFilesystem, path string) ([]DirectoryEntry, *DirectoryReadDiagnostic, *DirectoryReadTimings, error) {
	if reader, ok := filesystem.(timedDirectoryReader); ok {
		return reader.ReadDirWithTimings(path)
	}
	entries, diagnostic, err := ReadDirWithDiagnostics(filesystem, path)
	return entries, diagnostic, nil, err
}
