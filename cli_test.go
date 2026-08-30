package main

import (
	"strings"
	"testing"
)

func TestParseCommandLineDefaults(t *testing.T) {
	options, err := parseCommandLine(nil)
	if err != nil {
		t.Fatal(err)
	}
	if options.initialPath != "" || options.verbosity != defaultVerbosity || options.disableCache {
		t.Fatalf("unexpected defaults: %+v", options)
	}
}

func TestParseCommandLineDisablesCacheForStartupPath(t *testing.T) {
	options, err := parseCommandLine([]string{"--no-cache", `C:\Users`})
	if err != nil {
		t.Fatal(err)
	}
	if !options.disableCache || options.initialPath != `C:\Users` {
		t.Fatalf("unexpected options: %+v", options)
	}
	if _, err := parseCommandLine([]string{"--no-cache"}); err == nil {
		t.Fatal("--no-cache without a startup path was accepted")
	}
}

func TestParseCommandLinePathAndVerbosityInEitherOrder(t *testing.T) {
	for _, args := range [][]string{
		{`C:\Users`, "-v", "5"},
		{"--verbosity=5", `C:\Users`},
	} {
		options, err := parseCommandLine(args)
		if err != nil {
			t.Fatalf("parse %v: %v", args, err)
		}
		if options.initialPath != `C:\Users` || options.verbosity != verbosityTrace {
			t.Fatalf("unexpected options for %v: %+v", args, options)
		}
	}
}

func TestParseCommandLineRejectsInvalidArguments(t *testing.T) {
	for _, args := range [][]string{
		{"-v"},
		{"-v", "6"},
		{"--unknown"},
		{"first", "second"},
	} {
		if _, err := parseCommandLine(args); err == nil {
			t.Fatalf("expected %v to fail", args)
		}
	}
}

func TestSeverityLoggerFiltersByVerbosity(t *testing.T) {
	var output strings.Builder
	logger := NewSeverityLogger(verbosityWarning, &output)
	logger.Criticalf("critical")
	logger.Warningf("warning")
	logger.Infof("info")

	text := output.String()
	if !strings.Contains(text, "critical") || !strings.Contains(text, "warning") {
		t.Fatalf("expected important messages in %q", text)
	}
	if strings.Contains(text, "info") {
		t.Fatalf("unexpected filtered message in %q", text)
	}
}
