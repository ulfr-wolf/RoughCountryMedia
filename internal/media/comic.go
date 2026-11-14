package media

import (
	"archive/zip"
	"bytes"
	"errors"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/nwaples/rardecode/v2"
)

var imageExts = map[string]bool{".jpg": true, ".jpeg": true, ".png": true, ".gif": true, ".webp": true, ".avif": true, ".bmp": true}

func isImage(name string) bool {
	name = strings.ReplaceAll(name, `\`, "/")
	if strings.HasPrefix(name, "__MACOSX/") || strings.HasPrefix(path.Base(name), ".") {
		return false
	}
	return imageExts[strings.ToLower(path.Ext(name))]
}

var ErrUnsupportedArchive = errors.New("unsupported archive format")

func archiveType(p string) (string, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	head := make([]byte, 8)
	n, _ := io.ReadFull(f, head)
	head = head[:n]
	switch {
	case bytes.HasPrefix(head, []byte("PK\x03\x04")):
		return "zip", nil
	case bytes.HasPrefix(head, []byte("Rar!\x1a\x07")):
		return "rar", nil
	case bytes.HasPrefix(head, []byte("7z\xbc\xaf\x27\x1c")):
		return "7z", nil
	}
	return "", ErrUnsupportedArchive
}

// Comic gives page-by-page access to a CBZ or CBR archive. The extension is
// ignored: many .cbr files are really zips and vice versa.
type Comic struct {
	zr    *zip.ReadCloser
	files map[string]*zip.File
	dir   string // where a RAR archive was extracted
	Pages []string
}

// extractMu keeps RAR extraction to one archive at a time; it is CPU-heavy on the Mini.
var extractMu sync.Mutex

// OpenComic opens a comic. RAR archives can't be read page by page efficiently
// (most are "solid"), so they are extracted once into extractDir and reused.
func OpenComic(p, extractDir string) (*Comic, error) {
	kind, err := archiveType(p)
	if err != nil {
		return nil, err
	}
	switch kind {
	case "zip":
		zr, err := zip.OpenReader(p)
		if err != nil {
			return nil, err
		}
		c := &Comic{zr: zr, files: map[string]*zip.File{}}
		for _, f := range zr.File {
			if !f.FileInfo().IsDir() && isImage(f.Name) {
				c.files[f.Name] = f
				c.Pages = append(c.Pages, f.Name)
			}
		}
		sortNatural(c.Pages)
		return c, nil
	case "rar":
		if err := extractRar(p, extractDir); err != nil {
			return nil, err
		}
		c := &Comic{dir: extractDir}
		filepath.WalkDir(extractDir, func(fp string, d fs.DirEntry, err error) error {
			if err == nil && !d.IsDir() && isImage(d.Name()) {
				rel, _ := filepath.Rel(extractDir, fp)
				c.Pages = append(c.Pages, filepath.ToSlash(rel))
			}
			return nil
		})
		sortNatural(c.Pages)
		return c, nil
	}
	return nil, errors.New("CB7 comics can't be read in the browser yet; use Download")
}

func (c *Comic) Close() {
	if c.zr != nil {
		c.zr.Close()
	}
}

// Page opens page i (0-based) and returns its name for the content type.
func (c *Comic) Page(i int) (io.ReadCloser, string, error) {
	if i < 0 || i >= len(c.Pages) {
		return nil, "", fs.ErrNotExist
	}
	name := c.Pages[i]
	if c.zr != nil {
		rc, err := c.files[name].Open()
		return rc, name, err
	}
	f, err := os.Open(filepath.Join(c.dir, filepath.FromSlash(name)))
	return f, name, err
}

func extractRar(p, dir string) error {
	extractMu.Lock()
	defer extractMu.Unlock()
	if _, err := os.Stat(filepath.Join(dir, ".done")); err == nil {
		now := time.Now()
		os.Chtimes(dir, now, now) // mark as recently used so pruning keeps it
		return nil
	}
	tmp := dir + ".tmp"
	os.RemoveAll(tmp)
	if err := os.MkdirAll(tmp, 0o755); err != nil {
		return err
	}
	// Every write goes through an os.Root, so no entry can land outside tmp,
	// even via symlinks.
	root, err := os.OpenRoot(tmp)
	if err != nil {
		os.RemoveAll(tmp)
		return err
	}
	defer root.Close()
	r, err := rardecode.OpenReader(p)
	if err != nil {
		os.RemoveAll(tmp)
		return err
	}
	defer r.Close()
	fail := func(err error) error {
		root.Close()
		os.RemoveAll(tmp)
		return err
	}
	for {
		h, err := r.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fail(err)
		}
		if h.IsDir || !isImage(h.Name) {
			continue
		}
		name, ok := localArchivePath(h.Name)
		if !ok {
			continue // hostile entry name (absolute or "..") — skip it, don't rewrite it
		}
		rel := filepath.FromSlash(name)
		if err := root.MkdirAll(filepath.Dir(rel), 0o755); err != nil {
			return fail(err)
		}
		f, err := root.OpenFile(rel, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if errors.Is(err, fs.ErrExist) {
			continue // duplicate entry name: keep the first
		}
		if err != nil {
			return fail(err)
		}
		_, err = io.Copy(f, r)
		f.Close()
		if err != nil {
			return fail(err)
		}
	}
	root.WriteFile(".done", nil, 0o644)
	os.RemoveAll(dir)
	if err := os.Rename(tmp, dir); err != nil {
		return err
	}
	pruneDirs(filepath.Dir(dir), 8) // keep a handful of extracted comics, not the whole library
	return nil
}

// ComicCover returns the first page's bytes and name, for thumbnails. For RAR it
// takes the first image in archive order so it doesn't have to extract everything.
func ComicCover(p string) ([]byte, string, error) {
	kind, err := archiveType(p)
	if err != nil {
		return nil, "", err
	}
	const maxCover = 40 << 20
	switch kind {
	case "zip":
		c, err := OpenComic(p, "")
		if err != nil {
			return nil, "", err
		}
		defer c.Close()
		rc, name, err := c.Page(0)
		if err != nil {
			return nil, "", err
		}
		defer rc.Close()
		b, err := io.ReadAll(io.LimitReader(rc, maxCover))
		return b, name, err
	case "rar":
		r, err := rardecode.OpenReader(p)
		if err != nil {
			return nil, "", err
		}
		defer r.Close()
		for {
			h, err := r.Next()
			if err != nil {
				return nil, "", err
			}
			if !h.IsDir && isImage(h.Name) {
				b, err := io.ReadAll(io.LimitReader(r, maxCover))
				return b, h.Name, err
			}
		}
	}
	return nil, "", ErrUnsupportedArchive
}
