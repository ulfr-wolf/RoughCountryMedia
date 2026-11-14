// Package media reads inside media files: comic archives, EPUBs, subtitles,
// chapters and thumbnails. It knows nothing about the catalog.
package media

import (
	"context"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

var (
	toolsOnce sync.Once
	ffmpegBin string
	probeBin  string
)

func tools() (ffmpeg, ffprobe string) {
	toolsOnce.Do(func() {
		ffmpegBin, _ = exec.LookPath("ffmpeg")
		probeBin, _ = exec.LookPath("ffprobe")
	})
	return ffmpegBin, probeBin
}

// HasFFmpeg reports whether ffmpeg is installed (needed for thumbnails and embedded subtitles).
func HasFFmpeg() bool { f, _ := tools(); return f != "" }

// run executes a tool with a timeout and returns its stdout.
func run(ctx context.Context, timeout time.Duration, bin string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return exec.CommandContext(ctx, bin, args...).Output()
}

// NaturalLess orders "page2" before "page10", ignoring case.
func NaturalLess(a, b string) bool {
	a, b = strings.ToLower(a), strings.ToLower(b)
	for a != "" && b != "" {
		if isDigit(a[0]) && isDigit(b[0]) {
			na, ra := splitDigits(a)
			nb, rb := splitDigits(b)
			ta, tb := strings.TrimLeft(na, "0"), strings.TrimLeft(nb, "0")
			if len(ta) != len(tb) {
				return len(ta) < len(tb)
			}
			if ta != tb {
				return ta < tb
			}
			a, b = ra, rb
			continue
		}
		if a[0] != b[0] {
			return a[0] < b[0]
		}
		a, b = a[1:], b[1:]
	}
	return len(a) < len(b)
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

func splitDigits(s string) (digits, rest string) {
	i := 0
	for i < len(s) && isDigit(s[i]) {
		i++
	}
	return s[:i], s[i:]
}

func sortNatural(names []string) {
	sort.SliceStable(names, func(i, j int) bool { return NaturalLess(names[i], names[j]) })
}

// cleanArchivePath makes an archive entry name safe to use as a relative path.
func cleanArchivePath(name string) string {
	name = strings.ReplaceAll(name, `\`, "/")
	return strings.TrimPrefix(path.Clean("/"+name), "/")
}

// pruneDirs keeps the `keep` most recently modified subfolders of root and deletes the rest.
func pruneDirs(root string, keep int) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}
	type d struct {
		name string
		mod  time.Time
	}
	var dirs []d
	for _, e := range entries {
		if !e.IsDir() || strings.HasSuffix(e.Name(), ".tmp") {
			continue
		}
		if info, err := e.Info(); err == nil {
			dirs = append(dirs, d{e.Name(), info.ModTime()})
		}
	}
	sort.Slice(dirs, func(i, j int) bool { return dirs[i].mod.After(dirs[j].mod) })
	for i := keep; i < len(dirs); i++ {
		os.RemoveAll(filepath.Join(root, dirs[i].name))
	}
}

// ContentType returns the MIME type for files served from inside archives.
func ContentType(name string) string {
	switch strings.ToLower(path.Ext(name)) {
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".avif":
		return "image/avif"
	case ".bmp":
		return "image/bmp"
	case ".svg":
		return "image/svg+xml"
	case ".xhtml", ".xht", ".xml":
		return "application/xhtml+xml; charset=utf-8"
	case ".html", ".htm":
		return "text/html; charset=utf-8"
	case ".css":
		return "text/css; charset=utf-8"
	case ".ttf":
		return "font/ttf"
	case ".otf":
		return "font/otf"
	case ".woff":
		return "font/woff"
	case ".woff2":
		return "font/woff2"
	}
	return "application/octet-stream"
}
