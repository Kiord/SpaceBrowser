_Uh no... my disk is full_

# <img src="web/logo.svg " height="30">  SpaceBrowser

A cross-platform file system visualizer.

SpaceBrowser is an independent open-source project heavily inspired by [SpaceMonger 1.4](https://github.com/seanofw/spacemonger1).

Usage:
```
go run .
```


<img src="web/screenshot.jpg ">

### Features
 - Treemap with SpaceMonger's style
 - Open folder in file system
 - Node selection
 - Navigation
    - Zoom full
    - Go to parent
    - backward/forward
 - Responsive

### TODO:
 - File system dialog to browse in FS
 - Display enhancement
   - Modification date
 - Make it actually cross-platform (Win+Linux at least)
   - Manage platform-specific profile
     - Excluded paths
     - Find actual root
     - Hidden files
 - Various optimizations
   - Avoid complete redraws
   - Use an actual rendering backend
