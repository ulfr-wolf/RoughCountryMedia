package library

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/dhowden/tag"
)

const (
	KindMovie     = "movie"
	KindEpisode   = "episode"
	KindMusic     = "music"
	KindAudiobook = "audiobook"
	KindBook      = "book"
	KindComic     = "comic"
	KindPhoto     = "photo"
)

// file families by extension; video splits into movie/episode after parsing.
const (
	famVideo     = "video"
	famAudio     = "audio"
	famAudiobook = "audiobook"
	famBook      = "book"
	famComic     = "comic"
	famPhoto     = "photo"
)

var familyByExt = map[string]string{
	".mp4": famVideo, ".m4v": famVideo, ".mkv": famVideo, ".avi": famVideo, ".mov": famVideo,
	".webm": famVideo, ".wmv": famVideo, ".mpg": famVideo, ".mpeg": famVideo, ".ts": famVideo, ".m2ts": famVideo,
	".mp3": famAudio, ".flac": famAudio, ".m4a": famAudio, ".aac": famAudio, ".ogg": famAudio,
	".oga": famAudio, ".opus": famAudio, ".wav": famAudio, ".wma": famAudio, ".aiff": famAudio, ".aif": famAudio,
	".m4b":  famAudiobook,
	".epub": famBook, ".pdf": famBook, ".mobi": famBook, ".azw3": famBook,
	".cbz": famComic, ".cbr": famComic, ".cb7": famComic,
	".jpg": famPhoto, ".jpeg": famPhoto, ".png": famPhoto, ".gif": famPhoto, ".webp": famPhoto, ".heic": famPhoto,
}

// Supported reports whether a file name has a media extension RCM indexes.
func Supported(name string) bool {
	return familyByExt[strings.ToLower(filepath.Ext(name))] != ""
}

// skipName reports files and folders the scanner ignores.
func skipName(name string) bool {
	if name == "" {
		return false
	}
	if strings.HasPrefix(name, ".") || strings.HasSuffix(name, ".part") {
		return true
	}
	switch name {
	case "@eaDir", "#recycle", "$RECYCLE.BIN", "System Volume Information", "lost+found":
		return true
	}
	return false
}

var (
	reSxxEyy  = regexp.MustCompile(`(?i)(?:^|[^a-z0-9])s(\d{1,2})[ ._-]*e(\d{1,3})(?:[^0-9]|$)`)
	reNxNN    = regexp.MustCompile(`(?i)(?:^|[^a-z0-9])(\d{1,2})x(\d{2,3})(?:[^0-9]|$)`)
	reYear    = regexp.MustCompile(`(?:^|[^0-9])((?:19|20)\d{2})(?:[^0-9]|$)`)
	reBracket = regexp.MustCompile(`\[[^\]]*\]|\{[^}]*\}`)
	reJunk    = regexp.MustCompile(`(?i)\b(2160p|1080p|720p|480p|4k|uhd|bluray|blu-ray|brrip|bdrip|webrip|web-dl|web|hdtv|dvdrip|remux|x264|x265|h\.?264|h\.?265|hevc|hdr|10bit|aac|dts|proper|repack|extended|unrated)\b`)
	reTrackNo = regexp.MustCompile(`^(\d{1,3})(?:[ ._-]+|$)`)
	reSeason  = regexp.MustCompile(`(?i)^(season|series|s)[ ._-]*\d+$|^specials$`)
	reSpaces  = regexp.MustCompile(`\s+`)
)

// cleanName turns "The.Matrix_1999" into "The Matrix 1999".
func cleanName(s string) string {
	s = reBracket.ReplaceAllString(s, " ")
	s = strings.ReplaceAll(s, "_", " ")
	if !strings.Contains(s, " ") {
		s = strings.ReplaceAll(s, ".", " ")
	}
	s = reSpaces.ReplaceAllString(s, " ")
	return strings.Trim(s, " -.()")
}

func sortKey(title string) string {
	t := strings.ToLower(strings.TrimSpace(title))
	for _, art := range []string{"the ", "a ", "an "} {
		if strings.HasPrefix(t, art) && len(t) > len(art) {
			return t[len(art):]
		}
	}
	return t
}

// parseMovie extracts a title and year from a file or folder name.
func parseMovie(name string) (string, int) {
	s := cleanName(name)
	year := 0
	// Use the last year-like number ("Blade Runner 2049 (2017)"), and only when a
	// title comes before it ("2001" alone is a title).
	if all := reYear.FindAllStringSubmatchIndex(s, -1); len(all) > 0 {
		if m := all[len(all)-1]; m[2] > 0 {
			year, _ = strconv.Atoi(s[m[2]:m[3]])
			s = s[:m[2]]
		}
	}
	if loc := reJunk.FindStringIndex(s); loc != nil && loc[0] > 0 {
		s = s[:loc[0]]
	}
	s = strings.Trim(s, " -.([")
	if s == "" {
		s = cleanName(name)
	}
	return s, year
}

// parseEpisode detects S01E02 / 1x02 patterns. Show falls back to folder names.
func parseEpisode(path, root string) (show string, season, episode int, title string, ok bool) {
	base := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	loc := reSxxEyy.FindStringSubmatchIndex(base)
	if loc == nil {
		loc = reNxNN.FindStringSubmatchIndex(base)
	}
	if loc == nil {
		return "", 0, 0, "", false
	}
	season, _ = strconv.Atoi(base[loc[2]:loc[3]])
	episode, _ = strconv.Atoi(base[loc[4]:loc[5]])
	show = cleanName(base[:loc[0]])
	if y := reYear.FindStringSubmatchIndex(show); y != nil && y[2] > 0 {
		show = strings.Trim(show[:y[2]], " -.(")
	}
	if show == "" {
		show = showFromFolders(path, root)
	}
	title = cleanName(base[loc[5]:])
	if l := reJunk.FindStringIndex(title); l != nil {
		title = strings.Trim(title[:l[0]], " -.")
	}
	if title == "" {
		title = "Episode " + strconv.Itoa(episode)
	}
	return show, season, episode, title, true
}

func showFromFolders(path, root string) string {
	dir := filepath.Dir(path)
	for dir != root && isWithin(dir, root) {
		name := filepath.Base(dir)
		if !reSeason.MatchString(name) && !genericFolders[strings.ToLower(name)] {
			t, _ := parseMovie(name)
			return t
		}
		dir = filepath.Dir(dir)
	}
	return "Unknown Show"
}

// readTags reads audio tags, tolerating corrupt files.
func readTags(path string) (m tag.Metadata) {
	defer func() {
		if recover() != nil {
			m = nil
		}
	}()
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	m, err = tag.ReadFrom(f)
	if err != nil {
		return nil
	}
	return m
}

// genericFolders are category folders that say nothing about artist, album or title.
var genericFolders = map[string]bool{
	"movies": true, "tv": true, "tv shows": true, "shows": true, "music": true, "audiobooks": true,
	"books": true, "ebooks": true, "comics": true, "photos": true, "pictures": true, "media": true,
	"video": true, "videos": true, "downloads": true,
}

// folderNames returns the parent and grandparent folder names when they are inside
// root and are not generic category folders.
func folderNames(path, root string) (parent, grand string) {
	name := func(dir string) string {
		if dir == root || !isWithin(dir, root) {
			return ""
		}
		b := filepath.Base(dir)
		if genericFolders[strings.ToLower(b)] {
			return ""
		}
		return b
	}
	p := filepath.Dir(path)
	parent = name(p)
	if parent != "" {
		grand = name(filepath.Dir(p))
	}
	return parent, grand
}

// ParseFile builds an Item from a path; it does not touch the database.
func ParseFile(path string, lib Library, fi os.FileInfo) *Item {
	ext := strings.ToLower(filepath.Ext(path))
	base := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	it := &Item{
		LibraryID: lib.ID,
		Path:      path,
		Ext:       strings.TrimPrefix(ext, "."),
		Size:      fi.Size(),
		MTime:     fi.ModTime().Unix(),
	}
	switch familyByExt[ext] {
	case famVideo:
		if show, s, e, t, ok := parseEpisode(path, lib.Path); ok {
			it.Kind, it.Show, it.Season, it.Episode, it.Title = KindEpisode, show, s, e, t
		} else {
			it.Kind = KindMovie
			it.Title, it.Year = parseMovie(base)
			// "Movie (1999)/movie.mkv": prefer the folder when it has the year and the file doesn't.
			if it.Year == 0 {
				if parent, _ := folderNames(path, lib.Path); parent != "" {
					if t, y := parseMovie(parent); y != 0 {
						it.Title, it.Year = t, y
					}
				}
			}
		}
	case famAudio, famAudiobook:
		it.Kind = KindMusic
		if familyByExt[ext] == famAudiobook {
			it.Kind = KindAudiobook
		}
		parent, grand := folderNames(path, lib.Path)
		if m := readTags(path); m != nil {
			it.Title = strings.TrimSpace(m.Title())
			it.Artist = strings.TrimSpace(m.Artist())
			it.AlbumArtist = strings.TrimSpace(m.AlbumArtist())
			it.Album = strings.TrimSpace(m.Album())
			it.Year = m.Year()
			it.Track, _ = m.Track()
		}
		if it.Title == "" {
			it.Title = cleanName(reTrackNo.ReplaceAllString(base, ""))
			if it.Title == "" {
				it.Title = cleanName(base)
			}
		}
		if it.Track == 0 {
			if m := reTrackNo.FindStringSubmatch(base); m != nil {
				it.Track, _ = strconv.Atoi(m[1])
			}
		}
		if it.Album == "" {
			it.Album = parent
			if it.Kind == KindAudiobook && it.Album == "" {
				it.Album = it.Title
			}
		}
		if it.Artist == "" {
			it.Artist = grand
		}
		if it.AlbumArtist == "" {
			it.AlbumArtist = it.Artist
		}
		if it.Album == "" {
			it.Album = "Unknown Album"
		}
		if it.AlbumArtist == "" {
			it.AlbumArtist = "Unknown Artist"
		}
	case famBook:
		it.Kind = KindBook
		it.Title, it.Year = parseMovie(base)
	case famComic:
		it.Kind = KindComic
		it.Title = cleanName(base)
		it.Album, _ = folderNames(path, lib.Path) // series folder
	case famPhoto:
		it.Kind = KindPhoto
		it.Title = base
		it.Album, _ = folderNames(path, lib.Path)
	default:
		return nil
	}
	if it.Title == "" {
		it.Title = base
	}
	it.SortTitle = sortKey(it.Title)
	return it
}

// ImportFolder is the top-level folder an imported file is sorted into.
func ImportFolder(relPath string) string {
	ext := strings.ToLower(filepath.Ext(relPath))
	switch familyByExt[ext] {
	case famVideo:
		if _, _, _, _, ok := parseEpisode("/x/"+relPath, "/x"); ok {
			return "TV"
		}
		return "Movies"
	case famAudio:
		return "Music"
	case famAudiobook:
		return "Audiobooks"
	case famBook:
		return "Books"
	case famComic:
		return "Comics"
	case famPhoto:
		return "Photos"
	}
	return ""
}
