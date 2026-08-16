//go:build linux

package fileicon

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	defaultIconSize = 32
	maximumIconSize = 4 << 20
)

type iconThemeDirectory struct {
	name      string
	size      int
	scale     int
	typeName  string
	minSize   int
	maxSize   int
	threshold int
}

type iconThemeDefinition struct {
	inherits    []string
	directories []iconThemeDirectory
}

type iconThemeResolver struct {
	roots        []string
	pixmapRoots  []string
	currentTheme string
	targetSize   int
	themes       map[string]iconThemeDefinition
}

func newIconThemeResolver() *iconThemeResolver {
	return &iconThemeResolver{
		roots:        linuxIconRoots(),
		pixmapRoots:  linuxPixmapRoots(),
		currentTheme: detectLinuxIconTheme(),
		targetSize:   defaultIconSize,
		themes:       make(map[string]iconThemeDefinition),
	}
}

func (r *iconThemeResolver) Resolve(names []string) (string, error) {
	cleaned := make([]string, 0, len(names))
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if filepath.IsAbs(name) {
			if _, err := os.Stat(name); err == nil {
				return name, nil
			}
			continue
		}
		if strings.ContainsAny(name, `/\\`) {
			continue
		}
		cleaned = append(cleaned, name)
	}
	for _, name := range cleaned {
		if path := r.resolveTheme(name, r.currentTheme, make(map[string]bool)); path != "" {
			return path, nil
		}
		if r.currentTheme != "hicolor" {
			if path := r.resolveTheme(name, "hicolor", make(map[string]bool)); path != "" {
				return path, nil
			}
		}
		if path := r.resolveUnthemed(name); path != "" {
			return path, nil
		}
	}
	return "", fmt.Errorf("%w: no icon matched %s in theme %s", ErrUnavailable, strings.Join(cleaned, ", "), r.currentTheme)
}

func (r *iconThemeResolver) resolveTheme(name, theme string, visited map[string]bool) string {
	if theme == "" || visited[theme] {
		return ""
	}
	visited[theme] = true
	definition, ok := r.loadTheme(theme)
	if !ok {
		return ""
	}
	if path := r.lookupInTheme(name, theme, definition); path != "" {
		return path
	}
	for _, inherited := range definition.inherits {
		if path := r.resolveTheme(name, inherited, visited); path != "" {
			return path
		}
	}
	return ""
}

func (r *iconThemeResolver) lookupInTheme(name, theme string, definition iconThemeDefinition) string {
	for _, directory := range definition.directories {
		if !directory.exactSize(r.targetSize) {
			continue
		}
		if path := r.iconInDirectory(theme, directory.name, name); path != "" {
			return path
		}
	}
	type candidate struct {
		path  string
		score int
		order int
	}
	var candidates []candidate
	for order, directory := range definition.directories {
		if path := r.iconInDirectory(theme, directory.name, name); path != "" {
			candidates = append(candidates, candidate{path: path, score: directory.sizeDistance(r.targetSize), order: order})
		}
	}
	if len(candidates) == 0 {
		return ""
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].score == candidates[j].score {
			return candidates[i].order < candidates[j].order
		}
		return candidates[i].score < candidates[j].score
	})
	return candidates[0].path
}

func (r *iconThemeResolver) iconInDirectory(theme, directory, name string) string {
	for _, root := range r.roots {
		base := filepath.Join(root, theme, filepath.FromSlash(directory), name)
		for _, extension := range []string{".png", ".svg"} {
			path := base + extension
			if info, err := os.Stat(path); err == nil && !info.IsDir() {
				return path
			}
		}
	}
	return ""
}

func (r *iconThemeResolver) resolveUnthemed(name string) string {
	for _, root := range append(append([]string(nil), r.roots...), r.pixmapRoots...) {
		for _, extension := range []string{".png", ".svg"} {
			path := filepath.Join(root, name+extension)
			if info, err := os.Stat(path); err == nil && !info.IsDir() {
				return path
			}
		}
	}
	return ""
}

func (r *iconThemeResolver) loadTheme(name string) (iconThemeDefinition, bool) {
	if definition, ok := r.themes[name]; ok {
		return definition, true
	}
	for _, root := range r.roots {
		sections, err := readINI(filepath.Join(root, name, "index.theme"))
		if err != nil {
			continue
		}
		metadata := sections["Icon Theme"]
		definition := iconThemeDefinition{inherits: splitCommaList(metadata["Inherits"])}
		for _, directoryName := range splitCommaList(metadata["Directories"]) {
			values := sections[directoryName]
			size := integerSetting(values["Size"], 0)
			if size <= 0 {
				continue
			}
			directory := iconThemeDirectory{
				name:      directoryName,
				size:      size,
				scale:     integerSetting(values["Scale"], 1),
				typeName:  values["Type"],
				minSize:   integerSetting(values["MinSize"], size),
				maxSize:   integerSetting(values["MaxSize"], size),
				threshold: integerSetting(values["Threshold"], 2),
			}
			definition.directories = append(definition.directories, directory)
		}
		r.themes[name] = definition
		return definition, true
	}
	return iconThemeDefinition{}, false
}

func (d iconThemeDirectory) exactSize(target int) bool {
	scale := max(d.scale, 1)
	switch strings.ToLower(d.typeName) {
	case "scalable":
		return target >= d.minSize*scale && target <= d.maxSize*scale
	case "threshold", "":
		return target >= (d.size-d.threshold)*scale && target <= (d.size+d.threshold)*scale
	default:
		return target == d.size*scale
	}
}

func (d iconThemeDirectory) sizeDistance(target int) int {
	if d.exactSize(target) {
		return 0
	}
	scale := max(d.scale, 1)
	minimum, maximum := d.size*scale, d.size*scale
	if strings.EqualFold(d.typeName, "scalable") {
		minimum, maximum = d.minSize*scale, d.maxSize*scale
	} else if strings.EqualFold(d.typeName, "threshold") || d.typeName == "" {
		minimum, maximum = (d.size-d.threshold)*scale, (d.size+d.threshold)*scale
	}
	if target < minimum {
		return minimum - target
	}
	return target - maximum
}

func linuxIconRoots() []string {
	var roots []string
	home, _ := os.UserHomeDir()
	dataHome := os.Getenv("XDG_DATA_HOME")
	if dataHome == "" && home != "" {
		dataHome = filepath.Join(home, ".local", "share")
	}
	if dataHome != "" {
		roots = append(roots, filepath.Join(dataHome, "icons"))
	}
	if home != "" {
		roots = append(roots, filepath.Join(home, ".icons"))
	}
	dataDirectories := os.Getenv("XDG_DATA_DIRS")
	if dataDirectories == "" {
		dataDirectories = "/usr/local/share:/usr/share"
	}
	for _, directory := range filepath.SplitList(dataDirectories) {
		if directory != "" {
			roots = append(roots, filepath.Join(directory, "icons"))
		}
	}
	return uniquePaths(roots)
}

func linuxPixmapRoots() []string {
	var roots []string
	dataDirectories := os.Getenv("XDG_DATA_DIRS")
	if dataDirectories == "" {
		dataDirectories = "/usr/local/share:/usr/share"
	}
	for _, directory := range filepath.SplitList(dataDirectories) {
		if directory != "" {
			roots = append(roots, filepath.Join(directory, "pixmaps"))
		}
	}
	return uniquePaths(roots)
}

func detectLinuxIconTheme() string {
	if theme := strings.TrimSpace(os.Getenv("SPACEBROWSER_ICON_THEME")); theme != "" {
		return theme
	}
	configHome := os.Getenv("XDG_CONFIG_HOME")
	if configHome == "" {
		if home, err := os.UserHomeDir(); err == nil {
			configHome = filepath.Join(home, ".config")
		}
	}
	desktop := strings.ToLower(os.Getenv("XDG_CURRENT_DESKTOP"))
	if strings.Contains(desktop, "kde") {
		if theme := iniSetting(filepath.Join(configHome, "kdeglobals"), "Icons", "Theme"); theme != "" {
			return theme
		}
	}
	for _, version := range []string{"gtk-4.0", "gtk-3.0"} {
		if theme := iniSetting(filepath.Join(configHome, version, "settings.ini"), "Settings", "gtk-icon-theme-name"); theme != "" {
			return theme
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 750*time.Millisecond)
	defer cancel()
	if output, err := exec.CommandContext(ctx, "gsettings", "get", "org.gnome.desktop.interface", "icon-theme").Output(); err == nil {
		if theme := strings.Trim(strings.TrimSpace(string(output)), "'\""); theme != "" {
			return theme
		}
	}
	if strings.Contains(desktop, "kde") {
		return "breeze"
	}
	return "hicolor"
}

func readIconFile(path string) (Icon, error) {
	file, err := os.Open(path)
	if err != nil {
		return Icon{}, fmt.Errorf("open themed icon: %w", err)
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maximumIconSize+1))
	if err != nil {
		return Icon{}, fmt.Errorf("read themed icon: %w", err)
	}
	if len(data) == 0 || len(data) > maximumIconSize {
		return Icon{}, fmt.Errorf("themed icon has an invalid size")
	}
	mediaType := http.DetectContentType(data)
	if strings.EqualFold(filepath.Ext(path), ".svg") {
		mediaType = "image/svg+xml"
	}
	if !strings.HasPrefix(mediaType, "image/") {
		return Icon{}, fmt.Errorf("unsupported themed icon format %q", mediaType)
	}
	return Icon{Data: data, MediaType: mediaType}, nil
}

func readINI(path string) (map[string]map[string]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	sections := make(map[string]map[string]string)
	section := ""
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = strings.TrimSpace(line[1 : len(line)-1])
			if sections[section] == nil {
				sections[section] = make(map[string]string)
			}
			continue
		}
		key, value, found := strings.Cut(line, "=")
		if found && section != "" {
			sections[section][strings.TrimSpace(key)] = strings.TrimSpace(value)
		}
	}
	return sections, scanner.Err()
}

func iniSetting(path, section, key string) string {
	sections, err := readINI(path)
	if err != nil {
		return ""
	}
	return strings.Trim(strings.TrimSpace(sections[section][key]), "'\"")
}

func splitCommaList(value string) []string {
	var result []string
	for _, entry := range strings.Split(value, ",") {
		if entry = strings.TrimSpace(entry); entry != "" {
			result = append(result, entry)
		}
	}
	return result
}

func integerSetting(value string, fallback int) int {
	parsed, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		return fallback
	}
	return parsed
}

func uniquePaths(paths []string) []string {
	seen := make(map[string]bool)
	result := make([]string, 0, len(paths))
	for _, path := range paths {
		path = filepath.Clean(path)
		if !seen[path] {
			seen[path] = true
			result = append(result, path)
		}
	}
	return result
}
