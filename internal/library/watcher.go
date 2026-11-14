package library

import (
	"context"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

// debounce waits for a file to stop changing (e.g. a long copy) before indexing it.
const debounce = 3 * time.Second

type watcher struct {
	m       *Manager
	w       *fsnotify.Watcher
	mu      sync.Mutex
	pending map[string]*time.Timer
	warned  bool
}

func newWatcher(m *Manager) (*watcher, error) {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	return &watcher{m: m, w: w, pending: map[string]*time.Timer{}}, nil
}

// addTree watches root and every folder below it (fsnotify is not recursive).
func (wt *watcher) addTree(root string) {
	filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || !d.IsDir() {
			return nil
		}
		if p != root && skipName(d.Name()) {
			return filepath.SkipDir
		}
		if err := wt.w.Add(p); err != nil && !wt.warned {
			wt.warned = true
			log.Printf("watch %s: %v (on Linux, raise fs.inotify.max_user_watches)", p, err)
		}
		return nil
	})
}

func (wt *watcher) run(ctx context.Context) {
	defer wt.w.Close()
	for {
		select {
		case <-ctx.Done():
			return
		case err, ok := <-wt.w.Errors:
			if !ok {
				return
			}
			log.Printf("watcher: %v", err)
		case ev, ok := <-wt.w.Events:
			if !ok {
				return
			}
			if ev.Op == fsnotify.Chmod || (skipName(filepath.Base(ev.Name)) && !ev.Has(fsnotify.Rename)) {
				continue
			}
			if fi, err := os.Stat(ev.Name); err == nil && fi.IsDir() {
				if !ev.Has(fsnotify.Create) {
					// A folder "changed" because its contents did; the files get their own events.
					continue
				}
				wt.addTree(ev.Name)
			}
			wt.schedule(ev.Name)
		}
	}
}

func (wt *watcher) schedule(path string) {
	wt.mu.Lock()
	defer wt.mu.Unlock()
	if t, ok := wt.pending[path]; ok {
		t.Reset(debounce)
		return
	}
	wt.pending[path] = time.AfterFunc(debounce, func() {
		wt.mu.Lock()
		delete(wt.pending, path)
		wt.mu.Unlock()
		wt.m.SyncPath(path)
	})
}
