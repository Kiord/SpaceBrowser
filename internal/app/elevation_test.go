package app

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestAdministratorRestartKeepsConfigurationAndSessionPolicy(t *testing.T) {
	a := newApp(filepath.Join(t.TempDir(), "a folder", "settings.json"))
	a.SetCachingEnabled(false)
	a.logger.verbosity = verbosityTrace
	launches, quits := 0, 0
	launch := func(args []string) error {
		launches++
		options, err := parseCommandLine(args)
		if err != nil {
			t.Fatal(err)
		}
		if options.settingsPath != a.GetSettingsPath() || options.initialPath != "" || !options.disableCache || options.verbosity != verbosityTrace {
			t.Fatalf("restart options = %+v", options)
		}
		return nil
	}
	for range 2 {
		if err := a.restartAsAdministrator(func() bool { return false }, launch, func() { quits++ }); err != nil {
			t.Fatal(err)
		}
	}
	if launches != 1 || quits != 1 {
		t.Fatalf("launches = %d, quits = %d", launches, quits)
	}
}

func TestAdministratorRestartFailureKeepsOriginalAppAndAllowsRetry(t *testing.T) {
	a := newApp(filepath.Join(t.TempDir(), "settings.json"))
	want := errors.New("UAC cancelled")
	quits := 0
	err := a.restartAsAdministrator(func() bool { return false }, func([]string) error { return want }, func() { quits++ })
	if !errors.Is(err, want) || quits != 0 || a.elevationRestarting {
		t.Fatalf("failed restart: error = %v, quits = %d, restarting = %v", err, quits, a.elevationRestarting)
	}
	if err := a.restartAsAdministrator(func() bool { return false }, func([]string) error { return nil }, func() { quits++ }); err != nil || quits != 1 {
		t.Fatalf("retry: error = %v, quits = %d", err, quits)
	}
}

func TestAdministratorRestartSkipsElevatedApps(t *testing.T) {
	a := newApp(filepath.Join(t.TempDir(), "settings.json"))
	if err := a.restartAsAdministrator(func() bool { return true }, func([]string) error {
		t.Fatal("already elevated app must not relaunch")
		return nil
	}, func() { t.Fatal("already elevated app must not quit") }); err != nil {
		t.Fatal(err)
	}
}

func TestAdministratorRestartBlocksPendingScansAndSnapshotWriters(t *testing.T) {
	for _, info := range []ScanJobInfo{{State: "queued"}, {State: "running"}, {State: "paused"}, {State: "completed", Saving: true}} {
		t.Run(info.State, func(t *testing.T) {
			a := newApp(filepath.Join(t.TempDir(), "settings.json"))
			a.jobs = &scanJobQueue{app: a, jobs: []*scanJob{{info: info}}}
			err := a.restartAsAdministrator(func() bool { return false }, func([]string) error {
				t.Fatal("must not restart during pending work")
				return nil
			}, func() { t.Fatal("must not quit during pending work") })
			if err == nil {
				t.Fatal("expected pending scan error")
			}
		})
	}
}
