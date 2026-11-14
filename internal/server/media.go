package server

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"

	"rcm/internal/library"
	"rcm/internal/media"
)

// Handlers for looking inside media: thumbnails, comic pages, EPUB chapters,
// subtitles and chapters.

func cacheKey(it library.Item) string { return fmt.Sprintf("%d-%d", it.ID, it.MTime) }

func (s *Server) thumb(w http.ResponseWriter, r *http.Request) {
	it, ok := s.lookup(w, r)
	if !ok {
		return
	}
	p, err := s.thumbs.Get(r.Context(), media.ThumbSource{
		ID: it.ID, MTime: it.MTime, Kind: it.Kind, Ext: it.Ext, Path: it.Path, Duration: it.Duration,
	})
	if err != nil {
		w.Header().Set("Cache-Control", "max-age=300")
		http.Error(w, "no thumbnail", http.StatusNotFound)
		return
	}
	w.Header().Set("Cache-Control", "max-age=3600")
	w.Header().Set("Content-Type", "image/jpeg")
	http.ServeFile(w, r, p)
}

func (s *Server) openComic(w http.ResponseWriter, r *http.Request) (*media.Comic, library.Item, bool) {
	it, ok := s.lookup(w, r)
	if !ok {
		return nil, it, false
	}
	if it.Kind != library.KindComic {
		writeErr(w, 400, "not a comic")
		return nil, it, false
	}
	c, err := media.OpenComic(it.Path, filepath.Join(s.cacheDir, "comics", cacheKey(it)))
	if err != nil {
		writeErr(w, 422, err.Error())
		return nil, it, false
	}
	return c, it, true
}

func (s *Server) comicPages(w http.ResponseWriter, r *http.Request) {
	c, it, ok := s.openComic(w, r)
	if !ok {
		return
	}
	defer c.Close()
	n := len(c.Pages)
	if n == 0 {
		writeErr(w, 422, "no pages found in this comic")
		return
	}
	s.m.Store.SetLength(it.ID, float64(n))
	writeJSON(w, 200, map[string]int{"pages": n})
}

func (s *Server) comicPage(w http.ResponseWriter, r *http.Request) {
	n, err := strconv.Atoi(r.PathValue("n"))
	if err != nil {
		writeErr(w, 400, "bad page")
		return
	}
	c, _, ok := s.openComic(w, r)
	if !ok {
		return
	}
	defer c.Close()
	rc, name, err := c.Page(n)
	if err != nil {
		writeErr(w, 404, "no such page")
		return
	}
	defer rc.Close()
	w.Header().Set("Content-Type", media.ContentType(name))
	w.Header().Set("Cache-Control", "max-age=86400")
	io.Copy(w, rc)
}

func (s *Server) openEpub(w http.ResponseWriter, r *http.Request) (*media.Epub, library.Item, bool) {
	it, ok := s.lookup(w, r)
	if !ok {
		return nil, it, false
	}
	if it.Ext != "epub" {
		writeErr(w, 400, "not an EPUB")
		return nil, it, false
	}
	e, err := media.OpenEpub(it.Path)
	if err != nil {
		writeErr(w, 422, "this EPUB couldn't be opened: "+err.Error())
		return nil, it, false
	}
	return e, it, true
}

// book returns an EPUB's reading order as URLs plus its table of contents.
func (s *Server) book(w http.ResponseWriter, r *http.Request) {
	e, it, ok := s.openEpub(w, r)
	if !ok {
		return
	}
	defer e.Close()
	spine := make([]string, len(e.Spine))
	for i, p := range e.Spine {
		parts := strings.Split(p, "/")
		for j := range parts {
			parts[j] = url.PathEscape(parts[j])
		}
		spine[i] = fmt.Sprintf("/api/items/%d/epub/%s", it.ID, strings.Join(parts, "/"))
	}
	toc := e.TOC
	if toc == nil {
		toc = []media.TOCEntry{}
	}
	s.m.Store.SetLength(it.ID, float64(len(spine)))
	writeJSON(w, 200, map[string]any{"title": e.Title, "author": e.Author, "spine": spine, "toc": toc})
}

// epubFile serves one file from inside an EPUB. Book content is untrusted, so
// scripts are blocked by CSP (and the reader's iframe is sandboxed too).
func (s *Server) epubFile(w http.ResponseWriter, r *http.Request) {
	e, _, ok := s.openEpub(w, r)
	if !ok {
		return
	}
	defer e.Close()
	name := r.PathValue("path")
	rc, err := e.Open(name)
	if err != nil {
		writeErr(w, 404, "not found in book")
		return
	}
	defer rc.Close()
	w.Header().Set("Content-Type", media.ContentType(name))
	w.Header().Set("Content-Security-Policy", "default-src 'self' data:; script-src 'none'; object-src 'none'; style-src 'self' 'unsafe-inline' data:; img-src 'self' data:; font-src 'self' data:")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "max-age=3600")
	io.Copy(w, rc)
}

func (s *Server) chapters(w http.ResponseWriter, r *http.Request) {
	it, ok := s.lookup(w, r)
	if !ok {
		return
	}
	ch, err := media.Chapters(r.Context(), it.Path)
	if err != nil {
		ch = []media.Chapter{}
	}
	writeJSON(w, 200, ch)
}

func (s *Server) subtitles(w http.ResponseWriter, r *http.Request) {
	it, ok := s.lookup(w, r)
	if !ok {
		return
	}
	writeJSON(w, 200, media.FindSubtitles(r.Context(), it.Path))
}

func (s *Server) subtitle(w http.ResponseWriter, r *http.Request) {
	it, ok := s.lookup(w, r)
	if !ok {
		return
	}
	data, err := media.SubtitleVTT(r.Context(), it.Path, r.PathValue("sid"), cacheKey(it), filepath.Join(s.cacheDir, "subs"))
	if err != nil {
		writeErr(w, 404, err.Error())
		return
	}
	w.Header().Set("Content-Type", "text/vtt; charset=utf-8")
	w.Header().Set("Cache-Control", "max-age=3600")
	w.Write(data)
}
