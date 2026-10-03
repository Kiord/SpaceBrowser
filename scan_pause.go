package main

import (
	"context"
	"sync"
	"sync/atomic"
	"time"
)

// scanPause gates work between filesystem calls; cancellation always wakes it.
// An OS call already in flight must return before its worker can pause.
type scanPause struct {
	mu        sync.Mutex
	paused    atomic.Bool
	resume    chan struct{}
	pausedAt  time.Time
	pausedFor time.Duration
}

func (p *scanPause) set(paused bool) {
	p.setAt(paused, time.Now())
}

func (p *scanPause) setAt(paused bool, now time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.paused.Load() == paused {
		return
	}
	if paused {
		p.pausedAt = now
		p.resume = make(chan struct{})
	} else {
		p.pausedFor += now.Sub(p.pausedAt)
		close(p.resume)
	}
	p.paused.Store(paused)
}

func (p *scanPause) elapsed(start, now time.Time) time.Duration {
	if p == nil {
		return now.Sub(start)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.paused.Load() {
		now = p.pausedAt
	}
	return max(0, now.Sub(start)-p.pausedFor)
}

func (p *scanPause) wait(ctx context.Context) error {
	for p != nil && p.paused.Load() {
		p.mu.Lock()
		resume := p.resume
		p.mu.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-resume:
		}
	}
	return ctx.Err()
}

func (a *App) SetScanPaused(generation uint64, paused bool) bool {
	a.scanMu.RLock()
	defer a.scanMu.RUnlock()
	if !a.scanActive || a.scanGeneration != generation || a.scanResultPublished {
		return false
	}
	a.scanPause.set(paused)
	return a.scanPause.paused.Load()
}
