package app

import (
	"encoding/json"
)

// Set once by Run before starting workers.
var wailsConfiguration []byte

func applicationVersion() string {
	var configuration struct {
		Info struct {
			ProductVersion string `json:"productVersion"`
		} `json:"info"`
	}
	if err := json.Unmarshal(wailsConfiguration, &configuration); err != nil || configuration.Info.ProductVersion == "" || configuration.Info.ProductVersion == "0.0.0" {
		return "dev"
	}
	return configuration.Info.ProductVersion
}
