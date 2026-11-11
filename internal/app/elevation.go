package app

import (
	"fmt"
	"runtime"
	"strconv"

	"spacebrowser/internal/platform"

	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

func (a *App) IsElevated() bool { return platform.IsElevated() }

func (a *App) RestartAsAdministrator() error {
	if runtime.GOOS != "windows" {
		return fmt.Errorf("administrator restart is only available on Windows")
	}
	if a.ctx == nil {
		return fmt.Errorf("app not initialized")
	}
	return a.restartAsAdministrator(platform.IsElevated, platform.RestartElevated, func() { wailsruntime.Quit(a.ctx) })
}

func (a *App) restartAsAdministrator(isElevated func() bool, launch func([]string) error, quit func()) error {
	a.elevationMu.Lock()
	defer a.elevationMu.Unlock()
	if a.elevationRestarting || isElevated() {
		return nil
	}
	a.scanMu.RLock()
	active := a.scanActive
	a.scanMu.RUnlock()
	for _, job := range a.GetScanJobs() {
		active = active || pendingJob(job.State) || job.Saving
	}
	if active {
		return fmt.Errorf("finish or cancel active scans and wait for saved scans before restarting as administrator")
	}
	// Keep the chosen configuration and session policy, without replaying a CLI
	// scan path and starting an unsolicited rescan in the new process.
	args := []string{"--settings", a.GetSettingsPath(), "--verbosity", strconv.Itoa(a.logger.verbosity)}
	if !a.GetCachingEnabled() {
		args = append(args, "--no-cache")
	}
	if err := launch(args); err != nil {
		return err
	}
	a.elevationRestarting = true
	quit()
	return nil
}
