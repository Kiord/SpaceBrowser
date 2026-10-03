package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestScanElapsedTimeExcludesEveryPause(t *testing.T) {
	pause := &scanPause{}
	start := time.Unix(100, 0)
	pause.setAt(true, start.Add(2*time.Second))
	if got := pause.elapsed(start, start.Add(20*time.Second)); got != 2*time.Second {
		t.Fatalf("paused timer advanced: %v", got)
	}
	pause.setAt(false, start.Add(20*time.Second))
	pause.setAt(true, start.Add(23*time.Second))
	if got := pause.elapsed(start, start.Add(40*time.Second)); got != 5*time.Second {
		t.Fatalf("second pause timer = %v", got)
	}
	pause.setAt(false, start.Add(40*time.Second))
	if got := pause.elapsed(start, start.Add(41*time.Second)); got != 6*time.Second {
		t.Fatalf("resumed timer = %v", got)
	}
}

func TestPausedScannerResumesOrCancels(t *testing.T) {
	for _, cancelWhilePaused := range []bool{false, true} {
		t.Run(map[bool]string{false: "resume", true: "cancel"}[cancelWhilePaused], func(t *testing.T) {
			path := t.TempDir()
			if err := os.WriteFile(filepath.Join(path, "file"), []byte("hello"), 0600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			s := NewScanner(defaultProfile(), 1)
			s.SetContext(ctx, nil)
			s.pause = &scanPause{}
			s.pause.set(true)
			done := make(chan error, 1)
			go func() {
				var files, dirs int64
				_, err := s.buildTree(path, 0, -1, &files, &dirs)
				done <- err
			}()
			select {
			case err := <-done:
				t.Fatalf("paused scan returned: %v", err)
			case <-time.After(30 * time.Millisecond):
			}
			if files, dirs := s.LiveCounts(); files != 0 || dirs != 0 {
				t.Fatal("paused scan started enumeration")
			}
			if cancelWhilePaused {
				cancel()
			} else {
				s.pause.set(false)
			}
			select {
			case err := <-done:
				if cancelWhilePaused && !errors.Is(err, context.Canceled) {
					t.Fatalf("expected cancellation, got %v", err)
				}
				if !cancelWhilePaused && err != nil {
					t.Fatal(err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("paused worker did not wake")
			}
		})
	}
}

func TestPauseIsScopedToScanAndSharedAcrossRefreshScanners(t *testing.T) {
	a := &App{}
	_, generation := a.beginScan("test")
	defer a.finishScan(generation)
	if !a.SetScanPaused(generation, true) {
		t.Fatal("pause not accepted")
	}
	for i := 0; i < 2; i++ {
		scanner := NewScanner(defaultProfile(), 1)
		a.attachScanner(generation, scanner)
		if scanner.pause != a.scanPause || !a.GetScanProgress().Paused {
			t.Fatal("refresh scanner lost pause state")
		}
	}
	if a.SetScanPaused(generation+1, false) || !a.GetScanProgress().Paused {
		t.Fatal("stale request changed pause state")
	}
	a.SetScanPaused(generation, false)
	if a.GetScanProgress().Paused {
		t.Fatal("resume failed")
	}
	a.finishScan(generation)
	if a.SetScanPaused(generation, true) {
		t.Fatal("finished scan accepted pause")
	}
}
