package main

import (
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

// SuggestFolders completes one directory level without scanning its contents.
func (a *App) SuggestFolders(query string) []string {
	results := []string{}
	if query == "" {
		return results
	}
	if query == "~" || strings.HasPrefix(query, "~/") || strings.HasPrefix(query, "~\\") {
		if home, err := os.UserHomeDir(); err == nil {
			if query == "~" {
				query = home + string(filepath.Separator)
			} else {
				query = home + query[1:]
			}
		}
	}
	if runtime.GOOS == "windows" && len(query) == 2 && query[1] == ':' {
		query += `\`
	}
	directory, prefix := filepath.Split(query)
	if directory == "" {
		return results
	}
	file, err := os.Open(directory)
	if err != nil {
		return results
	}
	defer file.Close()
	for {
		entries, err := file.ReadDir(256)
		for _, entry := range entries {
			name, match := entry.Name(), prefix
			if runtime.GOOS == "windows" {
				name, match = strings.ToLower(name), strings.ToLower(match)
			}
			if !strings.HasPrefix(name, match) {
				continue
			}
			path := filepath.Join(directory, entry.Name())
			isDir := entry.IsDir()
			if !isDir && entry.Type()&os.ModeSymlink != 0 {
				info, statErr := os.Stat(path)
				isDir = statErr == nil && info.IsDir()
			}
			if isDir {
				results = append(results, path+string(filepath.Separator))
			}
			if len(results) == 30 {
				sort.Strings(results)
				return results
			}
		}
		if err != nil {
			break
		}
	}
	sort.Strings(results)
	return results
}
