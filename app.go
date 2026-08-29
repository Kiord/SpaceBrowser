package main

import (
	"context"
	"os"
	"sync"
	"time"

	"spacebrowser/internal/fileicon"
	"spacebrowser/internal/nativeinput"
	"spacebrowser/internal/platform"

	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

type App struct {
	ctx                 context.Context
	initialScanPath     string
	logger              *SeverityLogger
	filesystem          platform.ScannerFilesystem
	desktop             platform.DesktopActions
	locations           platform.LocationProvider
	store               TreeStore
	scanCache           *scanCacheManager
	showFreeSpace       bool
	profile             Profile
	settingsPath        string
	defaultSettingsPath string
	settingsMu          sync.RWMutex
	iconServiceOnce     sync.Once
	iconService         *fileicon.Service

	scanMu         sync.RWMutex
	scanGeneration uint64
	scanActive     bool
	scanPath       string
	scanCancel     context.CancelFunc
	scanStartedAt  time.Time
	scanScanner    *Scanner
}

func NewApp() *App {
	return newAppWithLogger(NewSeverityLogger(defaultVerbosity, os.Stderr))
}

func newAppWithLogger(logger *SeverityLogger) *App {
	defaultPath, err := defaultSettingsPath()
	if err != nil {
		logger.Warningf("could not determine the default settings location: %v", err)
	}
	return newAppWithPathsAndLogger(configuredSettingsPath(defaultPath), defaultPath, logger)
}

func newApp(settingsPath string) *App {
	return newAppWithPaths(settingsPath, settingsPath)
}

func newAppWithPaths(settingsPath, defaultPath string) *App {
	return newAppWithPathsAndLogger(settingsPath, defaultPath, NewSeverityLogger(defaultVerbosity, os.Stderr))
}

func newAppWithPathsAndLogger(settingsPath, defaultPath string, logger *SeverityLogger) *App {
	return newAppWithDependencies(settingsPath, defaultPath, logger, platform.Impl, platform.Impl, platform.Impl)
}

func newAppWithDependencies(settingsPath, defaultPath string, logger *SeverityLogger, filesystem platform.ScannerFilesystem, desktop platform.DesktopActions, locations platform.LocationProvider) *App {
	if filesystem == nil {
		filesystem = platform.Impl
	}
	if desktop == nil {
		desktop = platform.Impl
	}
	if locations == nil {
		locations = platform.Impl
	}
	profile := *defaultProfile()
	if settingsPath != "" {
		if savedProfile, err := loadSettingsWithFilesystem(settingsPath, filesystem); err == nil {
			profile = savedProfile
		} else if !os.IsNotExist(err) {
			logger.Warningf("could not load settings from %s: %v; using defaults", settingsPath, err)
		}
	}
	app := &App{
		showFreeSpace:       true,
		profile:             profile,
		settingsPath:        settingsPath,
		defaultSettingsPath: defaultPath,
		logger:              logger,
		filesystem:          filesystem,
		desktop:             desktop,
		locations:           locations,
	}
	app.scanCache = newScanCacheManager(defaultPath, logger)
	return app
}

func (a *App) Startup(ctx context.Context) {
	a.ctx = ctx
	nativeinput.StartAuxiliaryMouseCapture(func(button int) {
		wailsruntime.EventsEmit(ctx, "controls:auxiliary-mouse", button)
	})
	a.logger.Debugf("application runtime initialized")
}

func (a *App) Shutdown(context.Context) {
	if a.scanCache != nil {
		a.scanCache.Close()
	}
	a.logger.Infof("SpaceBrowser stopped")
}

func (a *App) GetInitialScanPath() string {
	return a.initialScanPath
}
