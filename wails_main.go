// wails_main.go
package main

import (
	"embed"
	"fmt"
	"os"
	"path/filepath"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/logger"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	linuxoptions "github.com/wailsapp/wails/v2/pkg/options/linux"
)

//go:embed all:web
var assets embed.FS

//go:embed assets/appicon.png
var appIcon []byte

func main() {
	attachParentConsole()
	logOutput := terminalLogOutput()

	cliOptions, err := parseCommandLine(os.Args[1:])
	if err != nil {
		consoleLogger := NewSeverityLogger(defaultVerbosity, logOutput)
		consoleLogger.Criticalf("%v", err)
		fmt.Fprintln(logOutput, commandLineUsage(filepath.Base(os.Args[0])))
		os.Exit(2)
	}
	if cliOptions.showHelp {
		fmt.Fprintln(os.Stdout, commandLineUsage(filepath.Base(os.Args[0])))
		return
	}
	if cliOptions.showVersion {
		fmt.Fprintf(os.Stdout, "SpaceBrowser %s\n", applicationVersion())
		return
	}

	consoleLogger := NewSeverityLogger(cliOptions.verbosity, logOutput)
	app := newAppWithLogger(consoleLogger)
	app.initialScanPath = cliOptions.initialPath
	app.initialScanNoCache = cliOptions.disableCache
	consoleLogger.Infof("starting SpaceBrowser %s (verbosity %d)", applicationVersion(), cliOptions.verbosity)
	if cliOptions.initialPath != "" {
		consoleLogger.Infof("requested initial scan: %s", cliOptions.initialPath)
	}

	err = wails.Run(&options.App{
		Title:      fmt.Sprintf("SpaceBrowser %s", applicationVersion()),
		Width:      1200,
		Height:     800,
		OnStartup:  app.Startup,
		OnShutdown: app.Shutdown,
		Bind:       []interface{}{app},
		Logger:     consoleLogger,
		// Let the application logger apply the CLI verbosity consistently to
		// both Wails and SpaceBrowser messages.
		LogLevel:           logger.TRACE,
		LogLevelProduction: logger.TRACE,

		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		Linux: &linuxoptions.Options{
			Icon:        appIcon,
			ProgramName: "spacebrowser",
		},
	})
	if err != nil {
		consoleLogger.Criticalf("application failed: %v", err)
	}
}
