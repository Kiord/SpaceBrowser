package main

import (
	_ "embed"
	"encoding/json"
)

//go:embed wails.json
var wailsConfiguration []byte

func applicationVersion() string {
	var configuration struct {
		Info struct {
			ProductVersion string `json:"productVersion"`
		} `json:"info"`
	}
	if err := json.Unmarshal(wailsConfiguration, &configuration); err != nil || configuration.Info.ProductVersion == "" {
		return "unknown"
	}
	return configuration.Info.ProductVersion
}
