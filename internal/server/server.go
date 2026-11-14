package server

import (
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"rcm/internal/library"
	"rcm/internal/media"
)

type Server struct {
	m         *library.Manager
	importDir string
	cacheDir  string
	static    fs.FS
	thumbs    *media.Thumbs
}

// New creates the HTTP server. cacheDir holds thumbnails, extracted comics and subtitles;
// it can be deleted at any time and will be rebuilt.
func New(m *library.Manager, importDir, cacheDir string, static fs.FS) *Server {
	return &Server{m: m, importDir: importDir, cacheDir: cacheDir, static: static,
		thumbs: media.NewThumbs(filepath.Join(cacheDir, "thumbs"))}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/status", s.status)
	mux.HandleFunc("GET /api/stats", s.stats)
	mux.HandleFunc("GET /api/items", s.items)
	mux.HandleFunc("GET /api/items/{id}", s.item)
	mux.HandleFunc("GET /api/items/{id}/stream", s.stream)
	mux.HandleFunc("PUT /api/items/{id}/progress", s.progress)
	mux.HandleFunc("GET /api/items/{id}/thumb", s.thumb)
	mux.HandleFunc("GET /api/items/{id}/pages", s.comicPages)
	mux.HandleFunc("GET /api/items/{id}/pages/{n}", s.comicPage)
	mux.HandleFunc("GET /api/items/{id}/book", s.book)
	mux.HandleFunc("GET /api/items/{id}/epub/{path...}", s.epubFile)
	mux.HandleFunc("GET /api/items/{id}/chapters", s.chapters)
	mux.HandleFunc("GET /api/items/{id}/subtitles", s.subtitles)
	mux.HandleFunc("GET /api/items/{id}/subtitles/{sid}", s.subtitle)
	mux.HandleFunc("GET /api/continue", s.cont)
	mux.HandleFunc("GET /api/shows", s.shows)
	mux.HandleFunc("GET /api/albums", s.albums)
	mux.HandleFunc("GET /api/libraries", s.libraries)
	mux.HandleFunc("POST /api/libraries", s.addLibrary)
	mux.HandleFunc("DELETE /api/libraries/{id}", s.removeLibrary)
	mux.HandleFunc("POST /api/rescan", s.rescan)
	mux.HandleFunc("PUT /api/import", s.importFile)
	static := http.FileServerFS(s.static)
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		// Embedded files have no mod time, so make browsers revalidate and pick up new versions.
		w.Header().Set("Cache-Control", "no-cache")
		static.ServeHTTP(w, r)
	})
	return logRequests(mux)
}

// ---- helpers ----

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func pathID(r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	return id, err == nil && id > 0
}

func (s *Server) lookup(w http.ResponseWriter, r *http.Request) (library.Item, bool) {
	id, ok := pathID(r)
	if !ok {
		writeErr(w, http.StatusBadRequest, "bad id")
		return library.Item{}, false
	}
	it, err := s.m.Store.Item(id)
	if errors.Is(err, sql.ErrNoRows) {
		writeErr(w, http.StatusNotFound, "not found")
		return it, false
	} else if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return it, false
	}
	return it, true
}

type statusRecorder struct {
	http.ResponseWriter
	code int
}

func (r *statusRecorder) WriteHeader(code int) { r.code = code; r.ResponseWriter.WriteHeader(code) }

// Unwrap lets http.ResponseController reach the real writer.
func (r *statusRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

// ReadFrom keeps Go's sendfile fast path for streams, which spares the Mini's CPU.
func (r *statusRecorder) ReadFrom(src io.Reader) (int64, error) {
	if rf, ok := r.ResponseWriter.(io.ReaderFrom); ok {
		return rf.ReadFrom(src)
	}
	return io.Copy(r.ResponseWriter, src)
}

func logRequests(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, code: 200}
		h.ServeHTTP(rec, r)
		// Streams and static files are noisy; log only API calls that are not progress pings, plus errors.
		p := r.URL.Path
		quiet := strings.HasSuffix(p, "/progress") || strings.HasSuffix(p, "/stream") || strings.HasSuffix(p, "/thumb") ||
			strings.Contains(p, "/pages/") || strings.Contains(p, "/epub/")
		if (strings.HasPrefix(p, "/api/") && !quiet) || rec.code >= 500 {
			log.Printf("%s %s %d %s", r.Method, r.URL.Path, rec.code, time.Since(start).Round(time.Millisecond))
		}
	})
}

// ---- handlers ----

func (s *Server) status(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, s.m.Status())
}

func (s *Server) stats(w http.ResponseWriter, r *http.Request) {
	st, err := s.m.Store.Stats()
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, st)
}

func (s *Server) items(w http.ResponseWriter, r *http.Request) {
	v := r.URL.Query()
	q := library.Query{
		Q:           strings.TrimSpace(v.Get("q")),
		Show:        v.Get("show"),
		Album:       v.Get("album"),
		AlbumArtist: v.Get("album_artist"),
		Sort:        v.Get("sort"),
	}
	if k := v.Get("kind"); k != "" {
		q.Kinds = strings.Split(k, ",")
	}
	q.Limit, _ = strconv.Atoi(v.Get("limit"))
	q.Offset, _ = strconv.Atoi(v.Get("offset"))
	items, err := s.m.Store.Items(q)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, items)
}

func (s *Server) item(w http.ResponseWriter, r *http.Request) {
	if it, ok := s.lookup(w, r); ok {
		writeJSON(w, 200, it)
	}
}

// contentTypes covers formats mime.TypeByExtension misses or gets wrong for browsers.
var contentTypes = map[string]string{
	"mkv": "video/webm", // Chrome/Firefox play MKV with web codecs when labeled as WebM
	"m4v": "video/mp4", "mp4": "video/mp4", "mov": "video/mp4", "webm": "video/webm",
	"ts": "video/mp2t", "m2ts": "video/mp2t", "avi": "video/x-msvideo", "wmv": "video/x-ms-wmv",
	"mp3": "audio/mpeg", "m4a": "audio/mp4", "m4b": "audio/mp4", "aac": "audio/aac", "flac": "audio/flac",
	"ogg": "audio/ogg", "oga": "audio/ogg", "opus": "audio/ogg", "wav": "audio/wav",
	"epub": "application/epub+zip", "pdf": "application/pdf", "mobi": "application/x-mobipocket-ebook",
	"cbz": "application/vnd.comicbook+zip", "cbr": "application/vnd.comicbook-rar",
	"heic": "image/heic", "webp": "image/webp",
}

func (s *Server) stream(w http.ResponseWriter, r *http.Request) {
	it, ok := s.lookup(w, r)
	if !ok {
		return
	}
	f, err := os.Open(it.Path)
	if err != nil {
		writeErr(w, http.StatusNotFound, "file is missing or its drive is offline")
		return
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	ct := contentTypes[it.Ext]
	if ct == "" {
		ct = mime.TypeByExtension("." + it.Ext)
	}
	if ct != "" {
		w.Header().Set("Content-Type", ct)
	}
	disp := "inline"
	if r.URL.Query().Get("download") == "1" {
		disp = "attachment"
	}
	w.Header().Set("Content-Disposition", mime.FormatMediaType(disp, map[string]string{"filename": filepath.Base(it.Path)}))
	// ServeContent handles Range requests, which is what makes seeking work.
	http.ServeContent(w, r, filepath.Base(it.Path), fi.ModTime(), f)
}

func (s *Server) progress(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeErr(w, 400, "bad id")
		return
	}
	var body struct {
		Position float64 `json:"position"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&body); err != nil || body.Position < 0 {
		writeErr(w, 400, "bad body")
		return
	}
	if err := s.m.Store.SetProgress(id, body.Position); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) cont(w http.ResponseWriter, r *http.Request) {
	items, err := s.m.Store.Continue(20)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, items)
}

func (s *Server) shows(w http.ResponseWriter, r *http.Request) {
	out, err := s.m.Store.Shows()
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, out)
}

func (s *Server) albums(w http.ResponseWriter, r *http.Request) {
	out, err := s.m.Store.Albums()
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, out)
}

func (s *Server) libraries(w http.ResponseWriter, r *http.Request) {
	libs, err := s.m.Store.Libraries()
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	type libOut struct {
		library.Library
		Import bool `json:"import"`
	}
	out := []libOut{}
	for _, l := range libs {
		out = append(out, libOut{l, l.Path == s.importDir})
	}
	writeJSON(w, 200, out)
}

func (s *Server) addLibrary(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&body); err != nil || strings.TrimSpace(body.Path) == "" {
		writeErr(w, 400, "send {\"path\": \"/folder/to/add\"}")
		return
	}
	p := strings.TrimSpace(body.Path)
	if strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			p = filepath.Join(home, p[2:])
		}
	}
	lib, err := s.m.AddLibrary(p)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	writeJSON(w, 201, lib)
}

func (s *Server) removeLibrary(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeErr(w, 400, "bad id")
		return
	}
	if err := s.m.RemoveLibrary(id); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) rescan(w http.ResponseWriter, r *http.Request) {
	go s.m.ScanAll()
	writeJSON(w, 202, map[string]string{"status": "scan started"})
}
