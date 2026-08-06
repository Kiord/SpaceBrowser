# <img src="web/logo.svg" height="25" alt=""> SpaceBrowser

SpaceBrowser is a cross-platform disk space visualizer. It scans a folder or volume and displays its contents as a size-proportional [treemap](https://en.wikipedia.org/wiki/Treemapping), making large files and directories easy to identify.

The project is open source and inspired by [SpaceMonger 1.4](https://github.com/seanofw/spacemonger1).

![SpaceBrowser screenshot](assets/screenshot.jpg)

## Features

- Interactive treemap
- Navigation
- Optional free-space display
- Scan progress and cancellation
- Settings dialog
- Context actions
- Windows, Linux and macOS (not tested)


## Usage

Prebuilt versions for Windows, macOS, and Linux are available on the [Releases page](https://github.com/Kiord/SpaceBrowser/releases). Download the file for your platform and run it directly.

To build SpaceBrowser from source, install Go 1.25 and the [Wails v2 development dependencies](https://wails.io/docs/gettingstarted/installation/), then run:

```sh
git clone https://github.com/Kiord/SpaceBrowser.git
cd SpaceBrowser
go install github.com/wailsapp/wails/v2/cmd/wails@latest
wails build
```

The resulting application is placed in `build/bin`. For development with automatic reloads, use:

```sh
wails dev
```
