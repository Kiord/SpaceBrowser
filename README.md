
<div align="center">

  <p align="center">
    <img src="web/header.svg"
         alt="SpaceBrowser"
         width="450">
  </p>

  <p align="center">
    <a href="https://github.com/Kiord/SpaceBrowser/releases">
      <img src="https://img.shields.io/github/v/release/Kiord/SpaceBrowser?style=plastic&include_prereleases&label=latest" alt="Latest release">
    </a>
    <a href="LICENSE">
      <img src="https://img.shields.io/badge/license-MIT-blue?style=plastic" alt="MIT License">
    </a>
    <img src="https://img.shields.io/badge/Go-1.27-00ADD8?style=plastic&logo=go&logoColor=white" alt="Go 1.27">
    <a href="https://github.com/wailsapp/awesome-wails">
      <img src="https://img.shields.io/badge/🕶-Awesome_Wails-2EA44F?style=plastic" alt="Awesome Wails">
    </a>
    <br>
   <img src="https://img.shields.io/badge/Windows-supported-0078D4?style=plastic&logo=windows&logoColor=white" alt="Windows">
    <img src="https://img.shields.io/badge/Linux-supported-FCC624?style=plastic&logo=linux&logoColor=black" alt="Linux">
    <img src="https://img.shields.io/badge/macOS-untested-555555?style=plastic&logo=apple&logoColor=white" alt="macOS">

  </p>

</div>

<br>

Visualize disk usage with an interactive [treemap](https://en.wikipedia.org/wiki/Treemapping), quickly identify large files and folders, explore your filesystem, and reclaim storage space.

<div align="center">

<img src="assets/screenshot.jpg" alt="SpaceBrowser application screenshot">

<br>

**[Download SpaceBrowser](https://github.com/Kiord/SpaceBrowser/releases)**

</div>


## Features

SpaceBrowser recreates the main features of [SpaceMonger 1.4](https://github.com/seanofw/spacemonger1) in a modern, cross-platform application.

<details>
<summary>Show all features</summary>

### Visualization and scanning

- Size-proportional treemap for files and folders
- Locations sidebar for drives, volumes, and folders
- Optional free-space node for scanned volumes
- Configurable small-file aggregation threshold
- Animated live scan display
- Exclusions for paths, hidden files, symlinks, and network filesystems

### Navigation and actions

- In-treemap navigation with Back, Forward, Parent, and Root commands
- Hover details with full path, byte size, modification date, and system icon
- "Open" and "Open with…" commands for files and folders
- Move-to-trash and permanent deletion with protection for sensitive filesystem locations
- Empty trash command

### Customization

- Configurable rectangle color palettes, scale, shape, shading, and hover highlighting
- Rebindable keyboard shortcuts
- Persistent application settings

### Platforms

- **Windows:** Supported
- **Linux:** Supported
- **macOS:** Build available, but not yet tested

</details>

## Installation

Download the latest available version from the **[GitHub Releases](https://github.com/Kiord/SpaceBrowser/releases)** page.

| Platform | Available packages |
|---|---|
| Windows | MSI installer, portable EXE |
| Linux | DEB, RPM, portable binary |
| macOS | DMG, ZIP (untested) |

> **Note:** macOS builds are provided but have not yet been tested. Feedback and bug reports are welcome.


<details>
<summary>Build from source</summary>

### Prerequisites

- [Go 1.27.2](https://go.dev/dl/)
- [Wails v2 development dependencies](https://wails.io/docs/gettingstarted/installation/)

### Clone and install build tools

```sh
git clone https://github.com/Kiord/SpaceBrowser.git
cd SpaceBrowser
go install github.com/wailsapp/wails/v2/cmd/wails@v2.17.0
```

### Build

```sh
wails build
```

</details>

## Usage

Launch SpaceBrowser normally to select a location to scan, or provide a path as a command-line argument to begin scanning immediately.

<details>
<summary>Command-line options</summary>

```text
Usage: spacebrowser [path] [options]

Launch SpaceBrowser and optionally begin scanning path.

Options:
      --no-cache         Disable saved-scan loading/saving for this session
      --cache            Enable saved-scan caching (default)
  -v, --verbosity level  Logging verbosity: 0=critical, 1=error,
                         2=warning, 3=info, 4=debug, 5=trace (default 3)
  -h, --help             Show this help
      --version          Show the SpaceBrowser version
```

</details>


## Contributing

Contributions, bug reports and suggestions are welcome!

- [Report a bug or request a feature](https://github.com/Kiord/SpaceBrowser/issues)
- Share feedback, especially about Linux and macOS compatibility



