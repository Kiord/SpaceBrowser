package main

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"mime"
	"net/http"
	"os"
	"os/exec"
	"spacebrowser/internal/platform"
	"strconv"
	"time"

	"github.com/shirou/gopsutil/v3/disk"
)

//go:embed web/*
var embeddedWeb embed.FS

// ============
// In-memory Store
// ============

type TreeStore struct {
	root   *Node
	nodes  []*Node // dense: nodes[id] == *Node
	rootID int
}

var store TreeStore

// ============
// HTTP Server
// ============

func main() {
	mux := http.NewServeMux()

	// API endpoints
	mux.HandleFunc("/api/get_full_tree", handleGetFullTree) // scan & cache; returns {ok, root_id}
	mux.HandleFunc("/api/layout", handleLayout)             // layout by node_id & size
	mux.HandleFunc("/api/open_in_file_browser", handleOpenInFileBrowser)

	mime.AddExtensionType(".svg", "image/svg+xml") // ensure correct content-type for SVGs
	webRoot, err := fs.Sub(embeddedWeb, "web")
	if err != nil {
		log.Fatal(err)
	}
	mux.Handle("/", http.FileServer(http.FS(webRoot)))

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

// Scan & cache tree; reply with root_id
func handleGetFullTree(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	if path == "" {
		writeErr(w, http.StatusBadRequest, "missing 'path' query parameter")
		return
	}
	path = platform.Impl.Canonicalize(path)

	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		writeJSON(w, map[string]string{"error": fmt.Sprintf("Invalid path (%s)", path)}, http.StatusBadRequest)
		return
	}

	profile := defaultProfile()
	var fileCount int64
	var dirCount int64
	scanner := NewScanner(profile, 0)

	// Build with parentID = -1 for the root
	root, err := scanner.buildTree(path, 0, -1, &fileCount, &dirCount)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Add [Free Disk Space] as a child (no node_id)
	if platform.Impl.IsMountRoot(path) {
		if fs, err := disk.Usage(path); err == nil {
			free := &Node{
				ID:          -1,
				ParentID:    root.ID,
				Name:        "[Free Disk Space]",
				Size:        int64(fs.Free),
				IsFolder:    false,
				IsFreeSpace: true,
				Depth:       1,
			}
			root.Children = append(root.Children, free)
		}
	}

	root.FileCount = int(fileCount)
	root.FolderCount = int(dirCount)

	// Cache in memory
	store.root = root
	store.nodes = scanner.Nodes()
	store.rootID = root.ID

	writeJSON(w, map[string]any{
		"ok":      true,
		"root_id": root.ID,
	}, http.StatusOK)
}

// Layout for a subtree by node_id, size in pixels
func handleLayout(w http.ResponseWriter, r *http.Request) {
	nodeIDStr := r.URL.Query().Get("node_id")
	wq := r.URL.Query().Get("w")
	hq := r.URL.Query().Get("h")
	if nodeIDStr == "" || wq == "" || hq == "" {
		writeErr(w, http.StatusBadRequest, "missing node_id/w/h")
		return
	}

	nodeID, err := strconv.Atoi(nodeIDStr)
	if err != nil || nodeID < 0 || nodeID >= len(store.nodes) {
		writeErr(w, http.StatusBadRequest, "invalid node_id")
		return
	}
	n := store.nodes[nodeID]
	if n == nil {
		writeErr(w, http.StatusNotFound, "node not found")
		return
	}

	width, errW := strconv.Atoi(wq)
	height, errH := strconv.Atoi(hq)
	if errW != nil || errH != nil || width <= 0 || height <= 0 {
		writeErr(w, http.StatusBadRequest, "invalid w/h")
		return
	}

	rects := ComputeTreemapRects(n, float64(width), float64(height))
	writeJSON(w, map[string]any{"rects": rects}, http.StatusOK)
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
	if err := platform.Impl.OpenInFileBrowser(in.Path); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, map[string]string{"status": "ok"}, http.StatusOK)
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

// func openInFileBrowser(path string) error {
// 	switch runtime.GOOS {
// 	case "windows":
// 		return run("explorer", path)
// 	case "darwin":
// 		return run("open", path)
// 	default:
// 		candidates := [][]string{{"xdg-open", path}, {"nautilus", path}, {"dolphin", path}, {"thunar", path}}
// 		for _, c := range candidates {
// 			if err := run(c[0], c[1:]...); err == nil {
// 				return nil
// 			}
// 		}
// 		return errors.New("no file manager found (tried xdg-open, nautilus, dolphin, thunar)")
// 	}
// }

func run(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	return cmd.Run()
}
