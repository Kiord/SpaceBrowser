package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/shirou/gopsutil/v3/disk"
	"github.com/sqweek/dialog"
)

// =====================
// Data Model (JSON API)
// =====================

type Node struct {
	Name        string  `json:"name"`
	Size        int64   `json:"size"`
	IsFolder    bool    `json:"is_folder"`
	IsFreeSpace bool    `json:"is_free_space"`
	Depth       int     `json:"depth"`
	FullPath    string  `json:"full_path,omitempty"`
	Children    []*Node `json:"children"`
	// Only populated on root for convenience (UI uses these if present)
	FileCount   int `json:"file_count,omitempty"`
	FolderCount int `json:"folder_count,omitempty"`
}

// Scanning profile (ported from system_profile.py, simplified)
// -----------------------------------------------------------

type Profile struct {
	PlatformSystem string
	ExcludedPaths  []string
	SkipHidden     bool
	MinFileSize    int64
	FollowSymlinks bool
}

func defaultProfile(selectedPath string) *Profile {
	p := &Profile{
		PlatformSystem: runtime.GOOS, // "windows" | "darwin" | "linux"
		SkipHidden:     false,
		MinFileSize:    1024,
		FollowSymlinks: false,
	}

	switch runtime.GOOS {
	case "linux":
		p.ExcludedPaths = []string{"/proc", "/sys", "/dev", "/run", "/var/lib/docker", "/var/log/lastlog", "/snap"}
	case "darwin":
		p.ExcludedPaths = []string{"/System", "/private/var/vm", "/Volumes/MobileBackups", "/Library/Application Support/MobileSync/Backup"}
	case "windows":
		windir := os.Getenv("WINDIR")
		if windir == "" {
			windir = `C:\Windows`
		}
		p.ExcludedPaths = []string{
			`C:\$Recycle.Bin`,
			`C:\System Volume Information`,
			filepath.Join(windir, "WinSxS"),
			filepath.Join(windir, "Temp"),
		}
	}
	return p
}

// ============
// HTTP Server
// ============

func main() {
	mux := http.NewServeMux()

	// API endpoints
	mux.HandleFunc("/api/get_full_tree", handleGetFullTree)
	mux.HandleFunc("/api/open_in_file_browser", handleOpenInFileBrowser)
	mux.HandleFunc("/api/pick_folder", handlePickFolder)

	// Serve the existing web UI (same folder Eel used)
	webDir := http.Dir("web")
	mux.Handle("/", http.FileServer(webDir))

	addr := ":8000"
	log.Printf("Starting Go server at http://localhost%v", addr)
	if err := http.ListenAndServe(addr, logRequests(mux)); err != nil {
		log.Fatal(err)
	}
}

func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		log.Printf("%s %s %dms", r.Method, r.URL.Path, time.Since(start).Milliseconds())
	})
}

// ==================
// Handlers / Helpers
// ==================

func handleGetFullTree(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	if path == "" {
		writeErr(w, http.StatusBadRequest, "missing 'path' query parameter")
		return
	}

	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		writeJSON(w, map[string]string{"error": fmt.Sprintf("Invalid path (%s)", path)}, http.StatusOK)
		return
	}

	profile := defaultProfile(path)
	fileCount := 0
	dirCount := 0

	root, err := buildTree(profile, path, 0, &fileCount, &dirCount)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Augment with free space tile if scanning the mount root
	if isMountRoot(path) {
		if fs, err := disk.Usage(path); err == nil {
			free := &Node{Name: "[Free Disk Space]", Size: int64(fs.Free), IsFolder: false, IsFreeSpace: true, Depth: 0}
			root.Children = append(root.Children, free)
		}
	}

	// Fill counts for convenience
	root.FileCount = fileCount
	root.FolderCount = dirCount

	writeJSON(w, root, http.StatusOK)
}

func handleOpenInFileBrowser(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	type req struct {
		Path string `json:"path"`
	}
	var in req
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.Path == "" {
		writeErr(w, http.StatusBadRequest, "missing JSON body: {\"path\": \"...\"}")
		return
	}
	if err := openInFileBrowser(in.Path); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, map[string]string{"status": "ok"}, http.StatusOK)
}

func handlePickFolder(w http.ResponseWriter, r *http.Request) {
	path, err := dialog.Directory().Title("Select a folder").Browse()
	if err != nil {
		// User canceled -> return empty string (match eel behavior)
		writeJSON(w, map[string]string{"path": ""}, http.StatusOK)
		return
	}
	writeJSON(w, map[string]string{"path": path}, http.StatusOK)
}

func writeJSON(w http.ResponseWriter, v interface{}, status int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, map[string]string{"error": msg}, status)
}

// ==============================
// Tree Building (ported logic)
// ==============================

func buildTree(profile *Profile, path string, depth int, fileCount, dirCount *int) (*Node, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	root := &Node{Name: baseName(abs), Size: 0, IsFolder: true, Depth: depth, FullPath: abs, Children: []*Node{}}

	*dirCount++

	entries, err := os.ReadDir(abs)
	if err != nil {
		// unreadable directory -> return empty folder
		return root, nil
	}

	for _, de := range entries {
		full := filepath.Join(abs, de.Name())

		// Excludes
		if shouldExclude(profile, full) {
			continue
		}

		// Symlink handling
		if de.Type()&os.ModeSymlink != 0 {
			continue
		}

		// Hidden files
		if profile.SkipHidden && isHidden(full) {
			continue
		}

		info, err := de.Info()
		if err != nil {
			continue
		}

		if info.IsDir() {
			n, _ := buildTree(profile, full, depth+1, fileCount, dirCount)
			root.Children = append(root.Children, n)
			root.Size += n.Size
			continue
		}

		// regular files only & min size
		if !info.Mode().IsRegular() {
			continue
		}
		if profile.MinFileSize > 0 && info.Size() < profile.MinFileSize {
			continue
		}

		sz := info.Size() // use logical size; Python used on-disk when available
		child := &Node{Name: de.Name(), Size: sz, IsFolder: false, Depth: depth + 1}
		root.Children = append(root.Children, child)
		root.Size += sz
		*fileCount++
	}

	// Sort children by size desc (UI expects this)
	sort.Slice(root.Children, func(i, j int) bool { return root.Children[i].Size > root.Children[j].Size })
	return root, nil
}

func baseName(path string) string {
	b := filepath.Base(path)
	if b == "." || b == string(os.PathSeparator) || b == "" {
		if runtime.GOOS == "windows" {
			// best-effort root label (e.g., C:\)
			vol := filepath.VolumeName(path)
			if vol != "" {
				return vol + "\\"
			}
		}
		return "/"
	}
	return b
}

func shouldExclude(p *Profile, absPath string) bool {
	cmp := func(s string) string {
		if runtime.GOOS == "windows" {
			return strings.ToLower(s)
		}
		return s
	}
	ap := cmp(absPath)
	for _, ex := range p.ExcludedPaths {
		exAbs := cmp(ex)
		if ap == exAbs || strings.HasPrefix(ap, filepath.Clean(exAbs)+string(os.PathSeparator)) {
			return true
		}
	}
	return false
}

func isHidden(path string) bool {
	base := filepath.Base(path)
	if base == "" {
		return false
	}
	// Simple cross-platform heuristic: leading dot
	// (On Windows, we don't read FILE_ATTRIBUTE_HIDDEN to keep deps small.)
	return strings.HasPrefix(base, ".")
}

// ============================
// Open in system file browser
// ============================

func openInFileBrowser(path string) error {
	switch runtime.GOOS {
	case "windows":
		return run("explorer", path)
	case "darwin":
		return run("open", path)
	default:
		// Try common Linux file managers
		candidates := [][]string{{"xdg-open", path}, {"nautilus", path}, {"dolphin", path}, {"thunar", path}}
		for _, c := range candidates {
			if err := run(c[0], c[1:]...); err == nil {
				return nil
			}
		}
		return errors.New("no file manager found (tried xdg-open, nautilus, dolphin, thunar)")
	}
}

func run(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	return cmd.Run()
}

// ===============
// Mount root util
// ===============

func isMountRoot(path string) bool {
	path, _ = filepath.Abs(path)
	if runtime.GOOS == "windows" {
		vol := filepath.VolumeName(path)
		clean := filepath.Clean(path)
		return strings.EqualFold(clean, vol+"\\")
	}
	return filepath.Clean(path) == "/"
}
