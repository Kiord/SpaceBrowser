<p align="center">
  <img src="web/logo.svg" width="128" alt="SpaceBrowser logo">
</p>

<h1 align="center">SpaceBrowser</h1>

<p align="center">
  Cross-platform disk space visualizer
</p>

<br>

SpaceBrowser scans a folder or volume and displays its contents as a size-proportional [treemap](https://en.wikipedia.org/wiki/Treemapping), making large files and directories easy to identify.

Inspired by [SpaceMonger 1.4](https://github.com/seanofw/spacemonger1), SpaceBrowser is a free, cross-platform, open-source disk space analyzer and an alternative to WinDirStat, WizTree, TreeSize, DaisyDisk, and SquirrelDisk

<br>

![SpaceBrowser screenshot](assets/screenshot.jpg)

## Features

SpaceBrowser recreates the main features of SpaceMonger 1.4 in a modern, cross-platform application.

### Visualization and scanning

- Size-proportional treemap for files and folders
- Locations side bar for drives, volumes, and folders
- Optional free-space node for scanned volumes
- Configurable small-file aggregation threshold
- Animated live scan display
- Exclusions for paths, hidden files, symlinks, and network filesystems

### Navigation and actions

- In-treemap navigation with Back, Forward, Parent, and Root commands
- Hover details with full path, byte size, modification date, and system icon
- "Open" and "Open with ..." command for files and folders
- Move-to-trash and permanent deletion with protection sensitive filesystem locations. Empty trash can be triggered as well.

### Customization

- Rectangle color palettes, scale, shape, shading, and hover highlighting
- Rebindable shortcuts
- Persistent settings

### Platforms

- Windows and Linux support
- macOS build available but not yet tested


## Usage

Prebuilt versions are available on the [Releases page](https://github.com/Kiord/SpaceBrowser/releases): MSI and portable EXE for Windows, DEB/RPM packages and a portable binary for Linux, and DMG/ZIP packages for macOS.

SpaceBrowser can be launched in a terminal, see `--help`.

To build SpaceBrowser from source, install Go 1.27 and the [Wails v2 development dependencies](https://wails.io/docs/gettingstarted/installation/), then run:

```sh
git clone https://github.com/Kiord/SpaceBrowser.git
cd SpaceBrowser
go -C tools/wails install github.com/wailsapp/wails/v2/cmd/wails
```

The [pinned build tool](tools/wails/README.md) includes the Go analysis library needed for Go 1.27.2.

Build the application with:

```sh
wails build
```



## Trigger a new release

Create and push a version tag. The release build derives its version from the tag:

```sh
git tag -a vX.X.X -m "Release vX.X.X"
git push origin vX.X.X
```
