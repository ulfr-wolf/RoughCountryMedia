package library

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// Store is the SQLite-backed media catalog.
type Store struct{ db *sql.DB }

type Library struct {
	ID      int64  `json:"id"`
	Path    string `json:"path"`
	Name    string `json:"name"`
	AddedAt int64  `json:"added_at"`
	Online  bool   `json:"online"`
}

type Item struct {
	ID          int64   `json:"id"`
	LibraryID   int64   `json:"library_id"`
	Path        string  `json:"path"`
	Kind        string  `json:"kind"`
	Title       string  `json:"title"`
	SortTitle   string  `json:"-"`
	Year        int     `json:"year,omitempty"`
	Show        string  `json:"show,omitempty"`
	Season      int     `json:"season,omitempty"`
	Episode     int     `json:"episode,omitempty"`
	Artist      string  `json:"artist,omitempty"`
	AlbumArtist string  `json:"album_artist,omitempty"`
	Album       string  `json:"album,omitempty"`
	Track       int     `json:"track,omitempty"`
	Duration    float64 `json:"duration,omitempty"`
	Size        int64   `json:"size"`
	MTime       int64   `json:"mtime"`
	Ext         string  `json:"ext"`
	AddedAt     int64   `json:"added_at"`
	Position    float64 `json:"position,omitempty"`
}

type stamp struct{ size, mtime int64 }

const schema = `
CREATE TABLE IF NOT EXISTS libraries(
  id INTEGER PRIMARY KEY,
  path TEXT NOT NULL UNIQUE,
  name TEXT NOT NULL,
  added_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS items(
  id INTEGER PRIMARY KEY,
  library_id INTEGER NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
  path TEXT NOT NULL UNIQUE,
  kind TEXT NOT NULL,
  title TEXT NOT NULL,
  sort_title TEXT NOT NULL,
  year INTEGER NOT NULL DEFAULT 0,
  show TEXT NOT NULL DEFAULT '',
  season INTEGER NOT NULL DEFAULT 0,
  episode INTEGER NOT NULL DEFAULT 0,
  artist TEXT NOT NULL DEFAULT '',
  album_artist TEXT NOT NULL DEFAULT '',
  album TEXT NOT NULL DEFAULT '',
  track INTEGER NOT NULL DEFAULT 0,
  duration REAL NOT NULL DEFAULT 0,
  probed INTEGER NOT NULL DEFAULT 0,
  size INTEGER NOT NULL,
  mtime INTEGER NOT NULL,
  ext TEXT NOT NULL,
  added_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS items_kind  ON items(kind, sort_title);
CREATE INDEX IF NOT EXISTS items_lib   ON items(library_id);
CREATE INDEX IF NOT EXISTS items_show  ON items(show, season, episode);
CREATE INDEX IF NOT EXISTS items_album ON items(album_artist, album, track);
CREATE INDEX IF NOT EXISTS items_added ON items(added_at);
CREATE TABLE IF NOT EXISTS progress(
  item_id INTEGER PRIMARY KEY REFERENCES items(id) ON DELETE CASCADE,
  position REAL NOT NULL,
  updated_at INTEGER NOT NULL
);
`

func OpenStore(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	dsn := "file:" + path + "?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// One connection keeps SQLite simple and avoids lock contention; it is plenty for a home server.
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("init schema: %w", err)
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

// ---- libraries ----

func (s *Store) AddLibrary(path string) (Library, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return Library{}, err
	}
	abs = filepath.Clean(abs)
	fi, err := os.Stat(abs)
	if err != nil {
		return Library{}, fmt.Errorf("folder not found: %s", abs)
	}
	if !fi.IsDir() {
		return Library{}, fmt.Errorf("not a folder: %s", abs)
	}
	for _, l := range s.mustLibraries() {
		if l.Path == abs {
			return l, nil
		}
		if isWithin(abs, l.Path) {
			return Library{}, fmt.Errorf("%s is already inside library %s", abs, l.Path)
		}
		if isWithin(l.Path, abs) {
			return Library{}, fmt.Errorf("library %s is inside %s; remove it first", l.Path, abs)
		}
	}
	now := time.Now().Unix()
	res, err := s.db.Exec(`INSERT INTO libraries(path,name,added_at) VALUES(?,?,?)`, abs, filepath.Base(abs), now)
	if err != nil {
		return Library{}, err
	}
	id, _ := res.LastInsertId()
	return Library{ID: id, Path: abs, Name: filepath.Base(abs), AddedAt: now, Online: true}, nil
}

func (s *Store) Libraries() ([]Library, error) {
	rows, err := s.db.Query(`SELECT id,path,name,added_at FROM libraries ORDER BY path`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Library
	for rows.Next() {
		var l Library
		if err := rows.Scan(&l.ID, &l.Path, &l.Name, &l.AddedAt); err != nil {
			return nil, err
		}
		fi, err := os.Stat(l.Path)
		l.Online = err == nil && fi.IsDir()
		out = append(out, l)
	}
	return out, rows.Err()
}

func (s *Store) mustLibraries() []Library {
	l, _ := s.Libraries()
	return l
}

func (s *Store) RemoveLibrary(id int64) error {
	_, err := s.db.Exec(`DELETE FROM libraries WHERE id=?`, id)
	return err
}

// LibraryFor returns the library that contains path, if any.
func (s *Store) LibraryFor(path string) (Library, bool) {
	var best Library
	found := false
	for _, l := range s.mustLibraries() {
		if (path == l.Path || isWithin(path, l.Path)) && len(l.Path) > len(best.Path) {
			best, found = l, true
		}
	}
	return best, found
}

// isWithin reports whether p is strictly inside dir.
func isWithin(p, dir string) bool {
	return strings.HasPrefix(p, strings.TrimSuffix(dir, string(filepath.Separator))+string(filepath.Separator))
}

// ---- items ----

const upsertSQL = `INSERT INTO items(library_id,path,kind,title,sort_title,year,show,season,episode,artist,album_artist,album,track,duration,probed,size,mtime,ext,added_at)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(path) DO UPDATE SET library_id=excluded.library_id, kind=excluded.kind, title=excluded.title,
  sort_title=excluded.sort_title, year=excluded.year, show=excluded.show, season=excluded.season,
  episode=excluded.episode, artist=excluded.artist, album_artist=excluded.album_artist, album=excluded.album,
  track=excluded.track, duration=excluded.duration, probed=excluded.probed, size=excluded.size,
  mtime=excluded.mtime, ext=excluded.ext`

// UpsertItems inserts or updates items in one transaction.
func (s *Store) UpsertItems(items []*Item) error {
	if len(items) == 0 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := tx.Prepare(upsertSQL)
	if err != nil {
		return err
	}
	defer stmt.Close()
	now := time.Now().Unix()
	for _, it := range items {
		probed := 0
		if it.Duration > 0 || !needsProbe(it.Kind) {
			probed = 1
		}
		if _, err := stmt.Exec(it.LibraryID, it.Path, it.Kind, it.Title, it.SortTitle, it.Year, it.Show, it.Season,
			it.Episode, it.Artist, it.AlbumArtist, it.Album, it.Track, it.Duration, probed, it.Size, it.MTime, it.Ext, now); err != nil {
			return fmt.Errorf("upsert %s: %w", it.Path, err)
		}
	}
	return tx.Commit()
}

func (s *Store) stamps(libID int64) (map[string]stamp, error) {
	rows, err := s.db.Query(`SELECT path,size,mtime FROM items WHERE library_id=?`, libID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]stamp)
	for rows.Next() {
		var p string
		var st stamp
		if err := rows.Scan(&p, &st.size, &st.mtime); err != nil {
			return nil, err
		}
		out[p] = st
	}
	return out, rows.Err()
}

func (s *Store) DeletePaths(paths []string) error {
	if len(paths) == 0 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, p := range paths {
		if _, err := tx.Exec(`DELETE FROM items WHERE path=?`, p); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// DeleteTree removes the item at path and everything under it (for deleted folders).
func (s *Store) DeleteTree(path string) (int64, error) {
	esc := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(path)
	res, err := s.db.Exec(`DELETE FROM items WHERE path=? OR path LIKE ? ESCAPE '\'`, path, esc+string(filepath.Separator)+"%")
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

const itemCols = `i.id,i.library_id,i.path,i.kind,i.title,i.sort_title,i.year,i.show,i.season,i.episode,i.artist,i.album_artist,i.album,i.track,i.duration,i.size,i.mtime,i.ext,i.added_at,COALESCE(p.position,0)`

func scanItems(rows *sql.Rows) ([]Item, error) {
	defer rows.Close()
	out := []Item{}
	for rows.Next() {
		var it Item
		if err := rows.Scan(&it.ID, &it.LibraryID, &it.Path, &it.Kind, &it.Title, &it.SortTitle, &it.Year, &it.Show,
			&it.Season, &it.Episode, &it.Artist, &it.AlbumArtist, &it.Album, &it.Track, &it.Duration, &it.Size,
			&it.MTime, &it.Ext, &it.AddedAt, &it.Position); err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

func (s *Store) Item(id int64) (Item, error) {
	rows, err := s.db.Query(`SELECT `+itemCols+` FROM items i LEFT JOIN progress p ON p.item_id=i.id WHERE i.id=?`, id)
	if err != nil {
		return Item{}, err
	}
	items, err := scanItems(rows)
	if err != nil {
		return Item{}, err
	}
	if len(items) == 0 {
		return Item{}, sql.ErrNoRows
	}
	return items[0], nil
}

type Query struct {
	Kinds       []string
	Q           string
	Show        string
	AlbumArtist string
	Album       string
	Sort        string // "title" (default), "added", "year"
	Limit       int
	Offset      int
}

func (s *Store) Items(q Query) ([]Item, error) {
	var where []string
	var args []any
	if len(q.Kinds) > 0 {
		where = append(where, "i.kind IN ("+strings.TrimSuffix(strings.Repeat("?,", len(q.Kinds)), ",")+")")
		for _, k := range q.Kinds {
			args = append(args, k)
		}
	}
	if q.Q != "" {
		like := "%" + q.Q + "%"
		where = append(where, "(i.title LIKE ? OR i.show LIKE ? OR i.artist LIKE ? OR i.album LIKE ? OR i.album_artist LIKE ?)")
		args = append(args, like, like, like, like, like)
	}
	if q.Show != "" {
		where = append(where, "i.show=?")
		args = append(args, q.Show)
	}
	if q.Album != "" || q.AlbumArtist != "" {
		where = append(where, "i.album=? AND i.album_artist=?")
		args = append(args, q.Album, q.AlbumArtist)
	}
	sqlStr := `SELECT ` + itemCols + ` FROM items i LEFT JOIN progress p ON p.item_id=i.id`
	if len(where) > 0 {
		sqlStr += " WHERE " + strings.Join(where, " AND ")
	}
	switch {
	case q.Sort == "added":
		sqlStr += " ORDER BY i.added_at DESC, i.id DESC"
	case q.Sort == "year":
		sqlStr += " ORDER BY i.year DESC, i.sort_title"
	case q.Show != "":
		sqlStr += " ORDER BY i.season, i.episode, i.sort_title"
	case q.Album != "" || q.AlbumArtist != "":
		sqlStr += " ORDER BY i.track, i.sort_title"
	default:
		sqlStr += " ORDER BY i.sort_title, i.id"
	}
	limit := q.Limit
	if limit <= 0 || limit > 5000 {
		limit = 5000
	}
	sqlStr += fmt.Sprintf(" LIMIT %d OFFSET %d", limit, max(q.Offset, 0))
	rows, err := s.db.Query(sqlStr, args...)
	if err != nil {
		return nil, err
	}
	return scanItems(rows)
}

func (s *Store) Continue(limit int) ([]Item, error) {
	// Video/audio positions are seconds. Book/comic positions are progress units
	// (pages read, or chapter + fraction) and their duration is the page/chapter count.
	rows, err := s.db.Query(`SELECT `+itemCols+` FROM items i JOIN progress p ON p.item_id=i.id
		WHERE (i.kind IN ('movie','episode','audiobook') AND p.position > 30
		       AND (i.duration = 0 OR p.position < i.duration * 0.95))
		   OR (i.kind IN ('book','comic') AND p.position > 0
		       AND (i.duration = 0 OR p.position < i.duration * 0.99))
		ORDER BY p.updated_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	return scanItems(rows)
}

func (s *Store) SetProgress(id int64, pos float64) error {
	_, err := s.db.Exec(`INSERT INTO progress(item_id,position,updated_at) VALUES(?,?,?)
		ON CONFLICT(item_id) DO UPDATE SET position=excluded.position, updated_at=excluded.updated_at`,
		id, pos, time.Now().Unix())
	return err
}

// SetLength records a book's chapter count or a comic's page count (stored as duration).
func (s *Store) SetLength(id int64, n float64) error {
	_, err := s.db.Exec(`UPDATE items SET duration=? WHERE id=? AND duration<>?`, n, id, n)
	return err
}

type Show struct {
	Show     string `json:"show"`
	Episodes int    `json:"episodes"`
	Seasons  int    `json:"seasons"`
	AddedAt  int64  `json:"added_at"`
	CoverID  int64  `json:"cover_id"`
}

func (s *Store) Shows() ([]Show, error) {
	rows, err := s.db.Query(`SELECT show, COUNT(*), COUNT(DISTINCT season), MAX(added_at), MIN(id) FROM items
		WHERE kind='episode' GROUP BY show ORDER BY show COLLATE NOCASE`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Show{}
	for rows.Next() {
		var sh Show
		if err := rows.Scan(&sh.Show, &sh.Episodes, &sh.Seasons, &sh.AddedAt, &sh.CoverID); err != nil {
			return nil, err
		}
		out = append(out, sh)
	}
	return out, rows.Err()
}

type Album struct {
	AlbumArtist string `json:"album_artist"`
	Album       string `json:"album"`
	Tracks      int    `json:"tracks"`
	Year        int    `json:"year,omitempty"`
	AddedAt     int64  `json:"added_at"`
	CoverID     int64  `json:"cover_id"`
}

func (s *Store) Albums() ([]Album, error) {
	rows, err := s.db.Query(`SELECT album_artist, album, COUNT(*), MAX(year), MAX(added_at), MIN(id) FROM items
		WHERE kind='music' GROUP BY album_artist, album ORDER BY album_artist COLLATE NOCASE, album COLLATE NOCASE`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Album{}
	for rows.Next() {
		var a Album
		if err := rows.Scan(&a.AlbumArtist, &a.Album, &a.Tracks, &a.Year, &a.AddedAt, &a.CoverID); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Store) Stats() (map[string]int, error) {
	rows, err := s.db.Query(`SELECT kind, COUNT(*) FROM items GROUP BY kind`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var k string
		var n int
		if err := rows.Scan(&k, &n); err != nil {
			return nil, err
		}
		out[k] = n
	}
	return out, rows.Err()
}

// ---- probing ----

func needsProbe(kind string) bool {
	switch kind {
	case KindMovie, KindEpisode, KindMusic, KindAudiobook:
		return true
	}
	return false
}

type probeJob struct {
	id   int64
	path string
}

func (s *Store) unprobed(limit int) ([]probeJob, error) {
	rows, err := s.db.Query(`SELECT id,path FROM items WHERE probed=0 LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []probeJob
	for rows.Next() {
		var j probeJob
		if err := rows.Scan(&j.id, &j.path); err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

func (s *Store) setDuration(id int64, d float64) error {
	_, err := s.db.Exec(`UPDATE items SET duration=?, probed=1 WHERE id=?`, d, id)
	return err
}
