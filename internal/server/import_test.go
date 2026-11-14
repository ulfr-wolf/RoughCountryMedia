package server

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"rcm/internal/library"
)

// newTestServer returns a server whose import folder is <tmp>/import, plus a
// sibling <tmp>/outside folder that must never receive a file.
func newTestServer(t *testing.T) (h http.Handler, importDir, outside string) {
	t.Helper()
	base := t.TempDir()
	importDir = filepath.Join(base, "import")
	outside = filepath.Join(base, "outside")
	os.MkdirAll(importDir, 0o755)
	os.MkdirAll(outside, 0o755)
	store, err := library.OpenStore(filepath.Join(base, "rcm.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	if _, err := store.AddLibrary(importDir); err != nil {
		t.Fatal(err)
	}
	s := New(library.NewManager(store), importDir, filepath.Join(base, "cache"), fstest.MapFS{})
	return s.Handler(), importDir, outside
}

func put(h http.Handler, rawPath, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("PUT", "/api/import?path="+url.QueryEscape(rawPath), strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func assertEmpty(t *testing.T, dir string) {
	t.Helper()
	filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			t.Errorf("file escaped the import folder: %s", p)
		}
		return nil
	})
}

func TestImportRejectsTraversal(t *testing.T) {
	h, importDir, outside := newTestServer(t)
	for _, p := range []string{
		"../outside/evil.mp4",
		"../../evil.mp4",
		"Show/../../outside/evil.mp4",
		"Show/../evil.mp4", // stays inside after cleaning, but is still a traversal attempt
		"./evil.mp4",
		"a/./evil.mp4",
		"/etc/evil.mp4",
		`..\outside\evil.mp4`,
		`a\..\..\evil.mp4`,
		"C:/evil.mp4",
		"a//evil.mp4",
		"evil.mp4/",
		".hidden/evil.mp4",
		"ok/\x00evil.mp4",
		"ok/evil\n.mp4",
		"",
	} {
		rec := put(h, p, "data")
		if rec.Code != http.StatusBadRequest {
			t.Errorf("path %q: got %d, want 400 (%s)", p, rec.Code, rec.Body.String())
		}
	}
	assertEmpty(t, outside)
	assertEmpty(t, importDir)
}

func TestImportRefusesSymlinks(t *testing.T) {
	h, importDir, outside := newTestServer(t)

	// A category folder that is a symlink out of the import folder.
	os.Symlink(outside, filepath.Join(importDir, "Movies"))
	if rec := put(h, "Heat (1995).mp4", "data"); rec.Code != http.StatusForbidden {
		t.Errorf("symlinked Movies/: got %d, want 403 (%s)", rec.Code, rec.Body.String())
	}

	// A symlink deeper down the path.
	os.MkdirAll(filepath.Join(importDir, "TV"), 0o755)
	os.Symlink(outside, filepath.Join(importDir, "TV", "Firefly"))
	if rec := put(h, "Firefly/Season 1/Firefly.S01E01.mkv", "data"); rec.Code != http.StatusForbidden {
		t.Errorf("symlinked show folder: got %d, want 403 (%s)", rec.Code, rec.Body.String())
	}

	// A relative symlink that stays inside the import folder: os.Root alone would
	// allow it, so this checks the explicit no-symlinks rule. RCM never creates links.
	os.MkdirAll(filepath.Join(importDir, "Music", "real"), 0o755)
	os.Symlink("real", filepath.Join(importDir, "Music", "Band"))
	if rec := put(h, "Band/song.mp3", "data"); rec.Code != http.StatusForbidden {
		t.Errorf("internal symlink: got %d, want 403", rec.Code)
	}
	assertEmpty(t, outside)
}

func TestImportNeverOverwritesThroughSymlinkedFile(t *testing.T) {
	h, importDir, outside := newTestServer(t)
	target := filepath.Join(outside, "victim.mp4")
	os.WriteFile(target, []byte("original"), 0o644)
	os.MkdirAll(filepath.Join(importDir, "Movies"), 0o755)
	os.Symlink(target, filepath.Join(importDir, "Movies", "Film (2001).mp4"))

	rec := put(h, "Film (2001).mp4", "attacker data")
	if rec.Code != http.StatusCreated {
		t.Fatalf("got %d: %s", rec.Code, rec.Body.String())
	}
	if b, _ := os.ReadFile(target); string(b) != "original" {
		t.Errorf("file outside the import folder was modified: %q", b)
	}
	if !strings.Contains(rec.Body.String(), "Film (2001) (1).mp4") {
		t.Errorf("expected a new unique name, got %s", rec.Body.String())
	}
}

func TestImportSavesAndKeepsExisting(t *testing.T) {
	h, importDir, _ := newTestServer(t)
	if rec := put(h, "Firefly/Season 1/Firefly.S01E01.Serenity.mkv", "one"); rec.Code != http.StatusCreated {
		t.Fatalf("got %d: %s", rec.Code, rec.Body.String())
	}
	if rec := put(h, "Firefly/Season 1/Firefly.S01E01.Serenity.mkv", "two"); rec.Code != http.StatusCreated {
		t.Fatalf("second upload: got %d", rec.Code)
	}
	dir := filepath.Join(importDir, "TV", "Firefly", "Season 1")
	if b, _ := os.ReadFile(filepath.Join(dir, "Firefly.S01E01.Serenity.mkv")); string(b) != "one" {
		t.Errorf("first file was overwritten: %q", b)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "Firefly.S01E01.Serenity (1).mkv")); string(b) != "two" {
		t.Errorf("second file missing: %q", b)
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".part") {
			t.Errorf("temporary file left behind: %s", e.Name())
		}
	}
	if rec := put(h, "notes.txt", "x"); rec.Code != 200 || !strings.Contains(rec.Body.String(), "skipped") {
		t.Errorf("non-media file: got %d %s", rec.Code, rec.Body.String())
	}
}
