package server

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"rcm/internal/library"
)

// importFile receives one dropped file as the raw request body:
//
//	PUT /api/import?path=Some%20Show/Season%201/ep.mkv
//
// The client-supplied path is untrusted. It is validated and rejected (never
// rewritten) if it tries to leave the import folder, every write goes through
// an os.Root confined to the import folder, symbolic links on the way are
// refused, and the final file's canonical path is checked to be inside the
// import folder. The body is streamed to disk, never held in RAM.
func (s *Server) importFile(w http.ResponseWriter, r *http.Request) {
	raw := r.URL.Query().Get("path")
	rel, err := validateRelPath(raw)
	if err != nil {
		log.Printf("import rejected from %s: %v (path %q)", r.RemoteAddr, err, raw)
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	folder := library.ImportFolder(rel)
	if folder == "" {
		// Not media (e.g. .nfo, .txt). Drain and skip so folder drops don't fail.
		io.Copy(io.Discard, r.Body)
		writeJSON(w, 200, map[string]any{"skipped": true, "reason": "not a supported media type"})
		return
	}

	root, err := os.OpenRoot(s.importDir)
	if err != nil {
		writeErr(w, 500, "import folder unavailable: "+err.Error())
		return
	}
	defer root.Close()

	destRel := filepath.Join(folder, filepath.FromSlash(rel))
	if err := refuseSymlinks(root, filepath.Dir(destRel)); err != nil {
		log.Printf("import rejected from %s: %v (path %q)", r.RemoteAddr, err, raw)
		writeErr(w, http.StatusForbidden, err.Error())
		return
	}
	// os.Root refuses to create or open anything outside the import folder, even
	// if a symlink is swapped in after the check above.
	if err := root.MkdirAll(filepath.Dir(destRel), 0o755); err != nil {
		writeErr(w, http.StatusForbidden, "can't create folder in the import folder: "+err.Error())
		return
	}

	tmpRel := destRel + "." + randomSuffix() + ".part" // ".part" files are ignored by the scanner
	f, err := root.OpenFile(tmpRel, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
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
		root.Remove(tmpRel)
		writeErr(w, 500, fmt.Sprintf("upload failed: %v", errors.Join(copyErr, closeErr)))
		return
	}
	finalRel, err := placeUnique(root, tmpRel, destRel)
	root.Remove(tmpRel)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}

	// Defense in depth: the file's real, symlink-resolved location must be inside the real import folder.
	dest := filepath.Join(s.importDir, finalRel)
	if err := s.insideImportDir(dest); err != nil {
		root.Remove(finalRel)
		log.Printf("import rejected from %s: %v (path %q)", r.RemoteAddr, err, raw)
		writeErr(w, http.StatusForbidden, err.Error())
		return
	}
	s.m.SyncPath(dest)
	log.Printf("imported %s (%d MB in %s)", dest, n>>20, time.Since(start).Round(time.Second))
	writeJSON(w, 201, map[string]any{"saved": dest, "bytes": n, "folder": folder})
}

var (
	errTraversal = errors.New("path must stay inside the import folder (no '..', '.', or absolute paths)")
	errBadName   = errors.New("path contains a name that isn't allowed")
)

// validateRelPath accepts only a plain relative path like "Show/Season 1/ep.mkv".
// Anything that could address a location outside the import folder is rejected outright.
func validateRelPath(p string) (string, error) {
	switch {
	case p == "":
		return "", errors.New("missing ?path=")
	case len(p) > 1024:
		return "", errors.New("path is too long")
	case strings.ContainsRune(p, '\\'):
		return "", errors.New("backslashes aren't allowed in paths")
	case strings.HasPrefix(p, "/") || filepath.IsAbs(p) || (len(p) >= 2 && p[1] == ':'):
		return "", errTraversal
	}
	for _, seg := range strings.Split(p, "/") {
		switch {
		case seg == "." || seg == "..":
			return "", errTraversal
		case seg == "":
			return "", errors.New("path has an empty folder name")
		case strings.HasPrefix(seg, "."):
			return "", errors.New("hidden files and folders are not imported")
		case len(seg) > 255:
			return "", errBadName
		}
		for _, c := range seg {
			if unicode.IsControl(c) {
				return "", errBadName
			}
		}
	}
	if !filepath.IsLocal(filepath.FromSlash(p)) {
		return "", errTraversal
	}
	return p, nil
}

// refuseSymlinks rejects the import if any existing folder on the way to dir
// (relative to the root) is a symbolic link. Nothing RCM creates is a symlink,
// so one here means the folder was tampered with or misconfigured.
func refuseSymlinks(root *os.Root, dir string) error {
	cur := ""
	for _, part := range strings.Split(filepath.ToSlash(dir), "/") {
		if part == "" || part == "." {
			continue
		}
		cur = filepath.Join(cur, part)
		fi, err := root.Lstat(cur)
		if errors.Is(err, fs.ErrNotExist) {
			return nil // the rest will be created fresh by MkdirAll
		}
		if err != nil {
			return fmt.Errorf("can't check %s: %w", cur, err)
		}
		if fi.Mode()&fs.ModeSymlink != 0 {
			return fmt.Errorf("refusing to write through symbolic link %q in the import folder", cur)
		}
		if !fi.IsDir() {
			return fmt.Errorf("%q in the import folder is a file, not a folder", cur)
		}
	}
	return nil
}

// placeUnique moves the finished upload to dest, or "dest (1)", "dest (2)", ...
// A hard link never replaces an existing file or symlink, so nothing is
// overwritten. Filesystems without hard links (exFAT, some network shares)
// fall back to a checked rename.
func placeUnique(root *os.Root, tmp, dest string) (string, error) {
	ext := filepath.Ext(dest)
	base := strings.TrimSuffix(dest, ext)
	for i := 0; i < 1000; i++ {
		cand := dest
		if i > 0 {
			cand = fmt.Sprintf("%s (%d)%s", base, i, ext)
		}
		err := root.Link(tmp, cand)
		if err == nil {
			return cand, nil
		}
		if errors.Is(err, fs.ErrExist) {
			continue
		}
		// No hard links here: rename, but only onto a name with nothing (not even a symlink) at it.
		if _, lerr := root.Lstat(cand); errors.Is(lerr, fs.ErrNotExist) {
			if rerr := root.Rename(tmp, cand); rerr != nil {
				return "", rerr
			}
			return cand, nil
		}
	}
	return "", errors.New("too many files with this name")
}

// insideImportDir checks that p's canonical (symlink-resolved) path is inside the import folder.
func (s *Server) insideImportDir(p string) error {
	real, err := filepath.EvalSymlinks(p)
	if err != nil {
		return fmt.Errorf("can't resolve saved file: %w", err)
	}
	if real != s.importCanon && !strings.HasPrefix(real, s.importCanon+string(filepath.Separator)) {
		return fmt.Errorf("saved file resolved outside the import folder")
	}
	return nil
}

func randomSuffix() string {
	b := make([]byte, 6)
	rand.Read(b)
	return hex.EncodeToString(b)
}
