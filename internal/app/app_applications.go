package app

import "spacebrowser/internal/platform"

type ApplicationInfo struct {
	Name    string `json:"name"`
	IconURL string `json:"iconUrl"`
}

func (a *App) GetDefaultApplicationInfo(path string) (ApplicationInfo, error) {
	if err := validateExistingPath(path); err != nil {
		return ApplicationInfo{}, err
	}
	name, _ := a.desktop.DefaultApplicationName(path)
	info := ApplicationInfo{Name: name}
	if provider, ok := a.desktop.(platform.ApplicationMetadata); ok {
		applicationPath, _ := provider.DefaultApplicationPath(path)
		if applicationPath != "" {
			info.IconURL, _ = a.GetAssociatedIcon(applicationPath, false)
		}
	}
	return info, nil
}

func (a *App) GetFileBrowserInfo() ApplicationInfo {
	provider, ok := a.desktop.(platform.ApplicationMetadata)
	if !ok {
		return ApplicationInfo{}
	}
	application, err := provider.FileBrowserApplication()
	if err != nil {
		return ApplicationInfo{}
	}
	info := ApplicationInfo{Name: application.Name}
	if application.Path != "" {
		info.IconURL, _ = a.GetAssociatedIcon(application.Path, false)
	}
	return info
}
