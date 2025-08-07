_Uh no... my disk is full_

# <img src="web/logo.svg " height="30">  SpaceBrowser

A cross-platform file system visualizer.

SpaceBrowser is an independent open-source project heavily inspired by [SpaceMonger 1.4](https://github.com/seanofw/spacemonger1) from [Sean Werkema](https://www.werkema.com/).

Usage:
```
python main.py
```

The only dependancy is Eel for the Python/JS interface:
```
pip install eel
```


<img src="web/screenshot.jpg ">

### Features
 - Treemap with SpaceMonger's style
 - File system browsing
 - Open folder in file system
 - Node selection
 - Navigation
    - Zoom full
    - Go to parent
    - backward/forward
 - Responsive
 - Treemap caching

### TODO:
 - Display enhancement
   - Block size
   - Modification date
 - Make it actually cross-platform (Win+Linux at least)
 - Various optimizations
   - Avoid complete redraws
   - Use an actual rendering backend
 - Improve squarification
