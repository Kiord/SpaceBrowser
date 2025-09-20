// wails_main.go
package main

import (
	"embed"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
)

//go:embed all:web
var assets embed.FS

func main() {
	app := NewApp()

	err := wails.Run(&options.App{
		Title:     "SpaceBrowser",
		Width:     1200,
		Height:    800,
		OnStartup: app.Startup,
		Bind:      []interface{}{app},

		AssetServer: &assetserver.Options{
			Assets: assets,
		},
	})
	if err != nil {
		println("Error:", err.Error())
	}
}
