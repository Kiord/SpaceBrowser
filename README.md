# <img src="web/logo.svg " height="20">  SpaceBrowser

A cross-platform port of SpaceMonger written in Python and Javascript.

The only dependancy is Eel for the Python/JS interface. 
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