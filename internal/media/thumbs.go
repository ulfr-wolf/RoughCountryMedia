package media

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

var ErrNoThumb = errors.New("no thumbnail")

// Thumbs makes small JPEG previews with ffmpeg and caches them on disk.
// At most two are generated at once so a page full of new covers can't swamp the Mini.
type Thumbs struct {
	dir  string
	sem  chan struct{}
	mu   sync.Mutex
	busy map[string]chan struct{}
}

// ThumbSource describes the item a thumbnail is made from.
type ThumbSource struct {
	ID       int64
	MTime    int64
	Kind     string
	Ext      string
	Path     string
	Duration float64
}

func NewThumbs(dir string) *Thumbs {
	os.MkdirAll(dir, 0o755)
	return &Thumbs{dir: dir, sem: make(chan struct{}, 2), busy: map[string]chan struct{}{}}
}

// Get returns the path of a cached thumbnail, generating it on first use.
// Items with no usable image are remembered so they aren't retried on every request.
func (t *Thumbs) Get(ctx context.Context, src ThumbSource) (string, error) {
	if !HasFFmpeg() {
		return "", ErrNoThumb
	}
	key := fmt.Sprintf("%d-%d", src.ID, src.MTime)
	out := filepath.Join(t.dir, key+".jpg")
	none := out + ".none"
	for {
		if _, err := os.Stat(out); err == nil {
			return out, nil
		}
		if _, err := os.Stat(none); err == nil {
			return "", ErrNoThumb
		}
		t.mu.Lock()
		if ch, ok := t.busy[key]; ok {
			t.mu.Unlock()
			select {
			case <-ch:
				continue
			case <-ctx.Done():
				return "", ctx.Err()
			}
		}
		ch := make(chan struct{})
		t.busy[key] = ch
		t.mu.Unlock()

		// Finish even if this browser request goes away; the next view gets the cached file.
		err := t.generate(src, out)
		if err != nil {
			os.WriteFile(none, nil, 0o644)
		}
		t.mu.Lock()
		delete(t.busy, key)
		close(ch)
		t.mu.Unlock()
		if err != nil {
			return "", ErrNoThumb
		}
		return out, nil
	}
}

func (t *Thumbs) generate(src ThumbSource, out string) error {
	t.sem <- struct{}{}
	defer func() { <-t.sem }()
	ffmpeg, _ := tools()

	input := src.Path
	var pre []string
	switch src.Kind {
	case "photo":
	case "movie", "episode":
		at := 10.0
		if src.Duration > 0 {
			at = min(src.Duration*0.1, 300)
		}
		pre = []string{"-ss", strconv.FormatFloat(at, 'f', 1, 64)}
	case "music", "audiobook":
		if c := folderCover(src.Path); c != "" {
			input = c
		}
	case "comic", "book":
		var data []byte
		var name string
		var err error
		if src.Kind == "comic" {
			data, name, err = ComicCover(src.Path)
		} else if src.Ext == "epub" {
			data, name, err = epubCover(src.Path)
		} else {
			return ErrNoThumb
		}
		if err != nil || len(data) == 0 {
			return ErrNoThumb
		}
		tmp, err := os.CreateTemp(t.dir, "src-*"+strings.ToLower(path.Ext(name)))
		if err != nil {
			return err
		}
		defer os.Remove(tmp.Name())
		tmp.Write(data)
		tmp.Close()
		input = tmp.Name()
	default:
		return ErrNoThumb
	}

	tmpOut := strings.TrimSuffix(out, ".jpg") + ".tmp.jpg"
	args := append([]string{"-v", "error", "-y"}, pre...)
	args = append(args, "-i", input, "-an", "-frames:v", "1", "-update", "1",
		"-vf", "scale=w=480:h=480:force_original_aspect_ratio=decrease", "-q:v", "4", tmpOut)
	if _, err := run(context.Background(), 60*time.Second, ffmpeg, args...); err != nil {
		os.Remove(tmpOut)
		return err
	}
	if fi, err := os.Stat(tmpOut); err != nil || fi.Size() == 0 {
		os.Remove(tmpOut)
		return ErrNoThumb
	}
	return os.Rename(tmpOut, out)
}

// folderCover finds album art saved next to music files (cover.jpg, folder.jpg, ...).
func folderCover(file string) string {
	dir := filepath.Dir(file)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	want := map[string]bool{"cover": true, "folder": true, "front": true, "album": true, "albumart": true}
	for _, e := range entries {
		name := strings.ToLower(e.Name())
		ext := filepath.Ext(name)
		if (ext == ".jpg" || ext == ".jpeg" || ext == ".png" || ext == ".webp") && want[strings.TrimSuffix(name, ext)] {
			return filepath.Join(dir, e.Name())
		}
	}
	return ""
}

func epubCover(p string) ([]byte, string, error) {
	e, err := OpenEpub(p)
	if err != nil {
		return nil, "", err
	}
	defer e.Close()
	if e.Cover == "" {
		return nil, "", ErrNoThumb
	}
	rc, err := e.Open(e.Cover)
	if err != nil {
		return nil, "", err
	}
	defer rc.Close()
	data, err := readLimited(rc, 40<<20)
	return data, e.Cover, err
}
