package main

import (
	"fmt"
	"strconv"
	"strings"
)

type commandLineOptions struct {
	initialPath string
	verbosity   int
	showHelp    bool
	showVersion bool
}

func parseCommandLine(args []string) (commandLineOptions, error) {
	options := commandLineOptions{verbosity: defaultVerbosity}
	positionalOnly := false

	for i := 0; i < len(args); i++ {
		argument := args[i]
		if !positionalOnly {
			switch {
			case argument == "--":
				positionalOnly = true
				continue
			case argument == "-h" || argument == "--help":
				options.showHelp = true
				continue
			case argument == "--version":
				options.showVersion = true
				continue
			case argument == "-v" || argument == "--verbosity":
				if i+1 >= len(args) {
					return options, fmt.Errorf("%s requires a value from 0 to %d", argument, maximumVerbosity)
				}
				i++
				if err := setVerbosity(&options, args[i]); err != nil {
					return options, err
				}
				continue
			case strings.HasPrefix(argument, "-v="):
				if err := setVerbosity(&options, strings.TrimPrefix(argument, "-v=")); err != nil {
					return options, err
				}
				continue
			case strings.HasPrefix(argument, "--verbosity="):
				if err := setVerbosity(&options, strings.TrimPrefix(argument, "--verbosity=")); err != nil {
					return options, err
				}
				continue
			case strings.HasPrefix(argument, "-"):
				return options, fmt.Errorf("unknown option %q", argument)
			}
		}

		if options.initialPath != "" {
			return options, fmt.Errorf("only one scan path may be specified")
		}
		options.initialPath = argument
	}

	return options, nil
}

func setVerbosity(options *commandLineOptions, value string) error {
	verbosity, err := strconv.Atoi(value)
	if err != nil || verbosity < verbosityCritical || verbosity > maximumVerbosity {
		return fmt.Errorf("verbosity must be a number from 0 to %d", maximumVerbosity)
	}
	options.verbosity = verbosity
	return nil
}

func commandLineUsage(executable string) string {
	return fmt.Sprintf(`Usage: %s [path] [-v level]

Launch SpaceBrowser and optionally begin scanning path.

Options:
  -v, --verbosity level  Logging verbosity: 0=critical, 1=error,
                         2=warning, 3=info, 4=debug, 5=trace (default 3)
  -h, --help             Show this help
      --version          Show the SpaceBrowser version`, executable)
}
