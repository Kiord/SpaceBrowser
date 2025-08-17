package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"time"

	"github.com/shirou/gopsutil/v3/disk"
	"github.com/sqweek/dialog"
)

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
