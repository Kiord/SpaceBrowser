package main

import (
	"fmt"
	"io"
	"sync"
	"time"
)

const (
	verbosityCritical = iota
	verbosityError
	verbosityWarning
	verbosityInfo
	verbosityDebug
	verbosityTrace

	defaultVerbosity = verbosityInfo
	maximumVerbosity = verbosityTrace
)

// SeverityLogger writes application and Wails messages to the terminal. Lower
// severity numbers are more important; verbosity controls the highest number
// that is displayed.
type SeverityLogger struct {
	verbosity int
	output    io.Writer
	mu        sync.Mutex
}

func NewSeverityLogger(verbosity int, output io.Writer) *SeverityLogger {
	return &SeverityLogger{verbosity: verbosity, output: output}
}

func (l *SeverityLogger) log(level int, label, message string) {
	if l == nil || l.output == nil || level > l.verbosity {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	fmt.Fprintf(l.output, "%s [%-8s] %s\n", time.Now().Format("2006-01-02 15:04:05.000"), label, message)
}

func (l *SeverityLogger) logf(level int, label, format string, args ...any) {
	if l == nil || level > l.verbosity {
		return
	}
	l.log(level, label, fmt.Sprintf(format, args...))
}

func (l *SeverityLogger) Criticalf(format string, args ...any) {
	l.logf(verbosityCritical, "CRITICAL", format, args...)
}

func (l *SeverityLogger) Errorf(format string, args ...any) {
	l.logf(verbosityError, "ERROR", format, args...)
}

func (l *SeverityLogger) Warningf(format string, args ...any) {
	l.logf(verbosityWarning, "WARNING", format, args...)
}

func (l *SeverityLogger) Infof(format string, args ...any) {
	l.logf(verbosityInfo, "INFO", format, args...)
}

func (l *SeverityLogger) Debugf(format string, args ...any) {
	l.logf(verbosityDebug, "DEBUG", format, args...)
}

func (l *SeverityLogger) Tracef(format string, args ...any) {
	l.logf(verbosityTrace, "TRACE", format, args...)
}

// The methods below implement Wails' logger.Logger interface, allowing Wails
// and SpaceBrowser to share one format and verbosity threshold.
func (l *SeverityLogger) Print(message string)   { l.log(verbosityInfo, "INFO", message) }
func (l *SeverityLogger) Println(message string) { l.Print(message) }
func (l *SeverityLogger) Trace(message string)   { l.log(verbosityTrace, "TRACE", message) }
func (l *SeverityLogger) Debug(message string)   { l.log(verbosityDebug, "DEBUG", message) }
func (l *SeverityLogger) Info(message string)    { l.log(verbosityInfo, "INFO", message) }
func (l *SeverityLogger) Warning(message string) { l.log(verbosityWarning, "WARNING", message) }
func (l *SeverityLogger) Error(message string)   { l.log(verbosityError, "ERROR", message) }
func (l *SeverityLogger) Fatal(message string)   { l.log(verbosityCritical, "CRITICAL", message) }
