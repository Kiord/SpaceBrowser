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
	"sync"
	"sync/atomic"
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

	profile := defaultProfile()

	var fileCount int64
	var dirCount int64

	// Tune the pool size. 0 = default (NumCPU*4). You can make this a flag/env.
	scanner := NewScanner(profile, 0)

	root, err := scanner.buildTree(path, 0, &fileCount, &dirCount)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}

	if isMountRoot(path) {
		if fs, err := disk.Usage(path); err == nil {
			free := &Node{Name: "[Free Disk Space]", Size: int64(fs.Free), IsFolder: false, IsFreeSpace: true, Depth: 0}
			root.Children = append(root.Children, free)
		}
	}

	root.FileCount = int(fileCount)
	root.FolderCount = int(dirCount)
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

type Scanner struct {
	profile    *Profile
	sem        chan struct{} // worker tokens
	maxWorkers int
}

// NewScanner(maxWorkers<=0 => sensible default)
func NewScanner(p *Profile, maxWorkers int) *Scanner {
	if maxWorkers <= 0 {
		maxWorkers = runtime.NumCPU() * 4 // good starting point for NVMe; tune for HDDs
	}
	return &Scanner{profile: p, sem: make(chan struct{}, maxWorkers), maxWorkers: maxWorkers}
}

// buildTree scans 'path' and all descendants.
// Concurrency: subdirectories of a folder are scanned in parallel, bounded by s.sem.
func (s *Scanner) buildTree(path string, depth int, fileCount, dirCount *int64) (*Node, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}

	// Count this directory
	atomic.AddInt64(dirCount, 1)

	root := &Node{
		Name:     baseName(abs),
		Size:     0,
		IsFolder: true,
		Depth:    depth,
		FullPath: abs,
		Children: make([]*Node, 0, 128),
	}

	entries, err := os.ReadDir(abs)
	if err != nil {
		// unreadable directory -> return empty folder
		return root, nil
	}

	// First pass: decide files vs subdirs; handle files immediately, queue subdirs.
	type subdir struct{ full string }
	subdirs := make([]subdir, 0, 32)

	for _, de := range entries {
		name := de.Name()
		full := filepath.Join(abs, name)

		if shouldExclude(s.profile, full) {
			continue
		}
		// Skip symlinks early (no Info() needed)
		if de.Type()&os.ModeSymlink != 0 {
			continue
		}
		// Hidden policy
		if s.profile.SkipHidden && isHidden(full) {
			continue
		}

		if de.IsDir() {
			// Defer scanning to worker pool
			subdirs = append(subdirs, subdir{full: full})
			continue
		}

		// For files we need size -> call Info() once
		info, err := de.Info()
		if err != nil {
			continue
		}
		if !info.Mode().IsRegular() {
			continue
		}
		if s.profile.MinFileSize > 0 && info.Size() < s.profile.MinFileSize {
			continue
		}

		sz := info.Size() // logical size; switch to on-disk if you add st_blocks
		child := &Node{Name: name, Size: sz, IsFolder: false, Depth: depth + 1}
		root.Children = append(root.Children, child)
		root.Size += sz
		atomic.AddInt64(fileCount, 1)
	}

	// Second pass: scan subdirectories in parallel (bounded).
	if len(subdirs) > 0 {

		var wg sync.WaitGroup
		var mu sync.Mutex
		results := make([]*Node, 0, len(subdirs))

		for _, sd := range subdirs {
			// Try to acquire a worker without blocking
			select {
			case s.sem <- struct{}{}:
				wg.Add(1)
				go func(p string) {
					defer wg.Done()
					defer func() { <-s.sem }()
					n, _ := s.buildTree(p, depth+1, fileCount, dirCount)
					mu.Lock()
					results = append(results, n)
					mu.Unlock()
				}(sd.full)
			default:
				// Pool is full — do it synchronously to avoid deadlock
				n, _ := s.buildTree(sd.full, depth+1, fileCount, dirCount)
				results = append(results, n)
			}
		}

		wg.Wait()
		for _, n := range results {
			root.Children = append(root.Children, n)
			root.Size += n.Size
		}
	}

	// Sort children by size desc (UI expects this)
	sort.Slice(root.Children, func(i, j int) bool { return root.Children[i].Size > root.Children[j].Size })
	return root, nil
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
