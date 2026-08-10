// wails_main.go
package main

import (
	"embed"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	linuxoptions "github.com/wailsapp/wails/v2/pkg/options/linux"
)

//go:embed all:web
var assets embed.FS

//go:embed assets/appicon.png
var appIcon []byte

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
		Linux: &linuxoptions.Options{
			Icon:        appIcon,
			ProgramName: "spacebrowser",
		},
	})
	if err != nil {
		println("Error:", err.Error())
	}
}
