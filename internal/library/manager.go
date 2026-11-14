package library

import (
	"context"
	"io/fs"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Manager owns scanning, watching and background probing of all libraries.
type Manager struct {
	Store *Store

	scanMu    sync.Mutex // one scan at a time keeps disk I/O gentle on the Mini
	scanning  atomic.Bool
	lastScan  atomic.Int64
	probeKick chan struct{}
	ffprobe   string
	watch     *watcher
}

type Status struct {
	Scanning   bool  `json:"scanning"`
	LastScan   int64 `json:"last_scan"`
	FFprobe    bool  `json:"ffprobe"`
	Watching   bool  `json:"watching"`
	TotalItems int   `json:"total_items"`
}

func NewManager(store *Store) *Manager {
	m := &Manager{Store: store, probeKick: make(chan struct{}, 1)}
	if p, err := exec.LookPath("ffprobe"); err == nil {
		m.ffprobe = p
	} else {
		log.Printf("ffprobe not found: durations will be missing (install ffmpeg)")
	}
	return m
}

// Start launches the watcher, the prober, an initial scan and a periodic safety rescan.
func (m *Manager) Start(ctx context.Context, rescanEvery time.Duration) {
	w, err := newWatcher(m)
	if err != nil {
		log.Printf("file watcher unavailable: %v (new files appear on rescan)", err)
	} else {
		m.watch = w
		go w.run(ctx)
		libs, _ := m.Store.Libraries()
		for _, l := range libs {
			w.addTree(l.Path)
		}
	}
	go m.probeLoop(ctx)
	go func() {
		m.ScanAll()
		if rescanEvery <= 0 {
			return
		}
		t := time.NewTicker(rescanEvery)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				m.ScanAll()
			}
		}
	}()
}

func (m *Manager) Status() Status {
	st := Status{Scanning: m.scanning.Load(), LastScan: m.lastScan.Load(), FFprobe: m.ffprobe != "", Watching: m.watch != nil}
	if counts, err := m.Store.Stats(); err == nil {
		for _, n := range counts {
			st.TotalItems += n
		}
	}
	return st
}

func (m *Manager) AddLibrary(path string) (Library, error) {
	lib, err := m.Store.AddLibrary(path)
	if err != nil {
		return lib, err
	}
	if m.watch != nil {
		m.watch.addTree(lib.Path)
	}
	go m.scanLibrary(lib)
	return lib, nil
}

func (m *Manager) RemoveLibrary(id int64) error { return m.Store.RemoveLibrary(id) }

// ScanAll scans every library. Safe to call concurrently; extra calls wait their turn.
func (m *Manager) ScanAll() {
	libs, err := m.Store.Libraries()
	if err != nil {
		log.Printf("scan: %v", err)
		return
	}
	for _, l := range libs {
		m.scanLibrary(l)
	}
}

func (m *Manager) scanLibrary(lib Library) {
	m.scanMu.Lock()
	defer m.scanMu.Unlock()
	m.scanning.Store(true)
	defer m.scanning.Store(false)

	start := time.Now()
	if fi, err := os.Stat(lib.Path); err != nil || !fi.IsDir() {
		// Drive unplugged or not mounted: keep the catalog rather than wiping it.
		log.Printf("scan: skipping offline library %s", lib.Path)
		return
	}
	existing, err := m.Store.stamps(lib.ID)
	if err != nil {
		log.Printf("scan %s: %v", lib.Path, err)
		return
	}
	seen, changed := m.indexTree(lib, lib.Path, existing)
	var gone []string
	for p := range existing {
		if !seen[p] {
			gone = append(gone, p)
		}
	}
	if err := m.Store.DeletePaths(gone); err != nil {
		log.Printf("scan %s: delete: %v", lib.Path, err)
	}
	m.lastScan.Store(time.Now().Unix())
	log.Printf("scan %s: %d files, %d new/changed, %d removed in %s", lib.Path, len(seen), changed, len(gone), time.Since(start).Round(time.Millisecond))
	m.kickProbe()
}

// indexTree walks root (inside lib) and upserts new or changed media files.
// existing may be nil, in which case every file found is upserted.
func (m *Manager) indexTree(lib Library, root string, existing map[string]stamp) (seen map[string]bool, changed int) {
	seen = make(map[string]bool)
	var batch []*Item
	flush := func() {
		if err := m.Store.UpsertItems(batch); err != nil {
			log.Printf("index: %v", err)
		}
		changed += len(batch)
		batch = batch[:0]
	}
	filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // unreadable entry: skip it, keep going
		}
		if p != root && skipName(d.Name()) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() || !d.Type().IsRegular() || !Supported(p) {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		seen[p] = true
		if st, ok := existing[p]; ok && st.size == info.Size() && st.mtime == info.ModTime().Unix() {
			return nil
		}
		if it := ParseFile(p, lib, info); it != nil {
			batch = append(batch, it)
			if len(batch) >= 500 {
				flush()
			}
		}
		return nil
	})
	flush()
	return seen, changed
}

// SyncPath brings the catalog in line with whatever is now at path (file or folder).
func (m *Manager) SyncPath(path string) {
	lib, ok := m.Store.LibraryFor(path)
	if !ok {
		return
	}
	fi, err := os.Stat(path)
	if err != nil {
		if n, err := m.Store.DeleteTree(path); err == nil && n > 0 {
			log.Printf("removed %d item(s) under %s", n, path)
		}
		return
	}
	if fi.IsDir() {
		existing, _ := m.Store.stamps(lib.ID) // skip files already indexed and unchanged
		_, n := m.indexTree(lib, path, existing)
		if n > 0 {
			log.Printf("indexed %d item(s) under %s", n, path)
		}
	} else if Supported(path) && !skipName(filepath.Base(path)) {
		if it := ParseFile(path, lib, fi); it != nil {
			if err := m.Store.UpsertItems([]*Item{it}); err != nil {
				log.Printf("index %s: %v", path, err)
			}
		}
	}
	m.kickProbe()
}

// ---- ffprobe ----

func (m *Manager) kickProbe() {
	select {
	case m.probeKick <- struct{}{}:
	default:
	}
}

func (m *Manager) probeLoop(ctx context.Context) {
	if m.ffprobe == "" {
		return
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-m.probeKick:
		}
		for ctx.Err() == nil {
			jobs, err := m.Store.unprobed(50)
			if err != nil || len(jobs) == 0 {
				break
			}
			for _, j := range jobs {
				if ctx.Err() != nil {
					return
				}
				d := m.probeDuration(ctx, j.path)
				m.Store.setDuration(j.id, d) // 0 on failure; marked probed so it isn't retried forever
			}
		}
	}
}

func (m *Manager) probeDuration(ctx context.Context, path string) float64 {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, m.ffprobe, "-v", "error", "-show_entries", "format=duration",
		"-of", "default=noprint_wrappers=1:nokey=1", path).Output()
	if err != nil {
		return 0
	}
	d, _ := strconv.ParseFloat(strings.TrimSpace(string(out)), 64)
	return d
}
