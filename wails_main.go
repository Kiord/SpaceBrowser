package main

import (
	"embed"

	"spacebrowser/internal/app"
)

//go:embed all:web
var assets embed.FS

//go:embed assets/appicon.png
var appIcon []byte

//go:embed wails.json
var configuration []byte

func main() {
	app.Run(assets, appIcon, configuration)
}
