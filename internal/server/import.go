package server

import (
	"bufio"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"rcm/internal/library"
)

// importFile receives one dropped file as the raw request body:
//
//	PUT /api/import?path=Some%20Show/Season%201/ep.mkv
//
// The file is streamed to disk (never held in RAM), written as .part and renamed
// when complete, then sorted under <import dir>/<Movies|TV|Music|...>/.
func (s *Server) importFile(w http.ResponseWriter, r *http.Request) {
	rel, err := safeRelPath(r.URL.Query().Get("path"))
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	folder := library.ImportFolder(rel)
	if folder == "" {
		// Not media (e.g. .nfo, .txt). Drain and skip so folder drops don't fail.
		io.Copy(io.Discard, r.Body)
		writeJSON(w, 200, map[string]any{"skipped": true, "reason": "not a supported media type"})
		return
	}
	dest := uniquePath(filepath.Join(s.importDir, folder, filepath.FromSlash(rel)))
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	tmp := dest + ".part"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	start := time.Now()
	bw := bufio.NewWriterSize(f, 1<<20)
	n, copyErr := io.Copy(bw, r.Body)
	if copyErr == nil {
		copyErr = bw.Flush()
	}
	closeErr := f.Close()
	if copyErr != nil || closeErr != nil {
		os.Remove(tmp)
		writeErr(w, 500, fmt.Sprintf("upload failed: %v", firstErr(copyErr, closeErr)))
		return
	}
	if err := os.Rename(tmp, dest); err != nil {
		os.Remove(tmp)
		writeErr(w, 500, err.Error())
		return
	}
	s.m.SyncPath(dest)
	log.Printf("imported %s (%d MB in %s)", dest, n>>20, time.Since(start).Round(time.Second))
	writeJSON(w, 201, map[string]any{"saved": dest, "bytes": n, "folder": folder})
}

// safeRelPath turns a client-supplied relative path into one that cannot escape the import dir.
func safeRelPath(p string) (string, error) {
	p = strings.ReplaceAll(p, `\`, "/")
	clean := strings.TrimPrefix(path.Clean("/"+p), "/")
	if clean == "" || clean == "." {
		return "", fmt.Errorf("missing ?path=")
	}
	for _, part := range strings.Split(clean, "/") {
		if strings.HasPrefix(part, ".") {
			return "", fmt.Errorf("hidden files and folders are not imported")
		}
	}
	return clean, nil
}

// uniquePath appends " (1)", " (2)", ... so imports never overwrite existing files.
func uniquePath(p string) string {
	if _, err := os.Stat(p); os.IsNotExist(err) {
		return p
	}
	ext := filepath.Ext(p)
	base := strings.TrimSuffix(p, ext)
	for i := 1; ; i++ {
		c := fmt.Sprintf("%s (%d)%s", base, i, ext)
		if _, err := os.Stat(c); os.IsNotExist(err) {
			return c
		}
	}
}

func firstErr(errs ...error) error {
	for _, e := range errs {
		if e != nil {
			return e
		}
	}
	return nil
}
