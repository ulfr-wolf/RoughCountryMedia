package library

import (
	"io/fs"
	"testing"
	"time"
)

type fakeInfo struct{ size int64 }

func (f fakeInfo) Name() string       { return "" }
func (f fakeInfo) Size() int64        { return f.size }
func (f fakeInfo) Mode() fs.FileMode  { return 0 }
func (f fakeInfo) ModTime() time.Time { return time.Unix(1700000000, 0) }
func (f fakeInfo) IsDir() bool        { return false }
func (f fakeInfo) Sys() any           { return nil }

func TestParseFile(t *testing.T) {
	lib := Library{ID: 1, Path: "/media"}
	cases := []struct {
		path               string
		kind, title, show  string
		year, season, ep   int
		album, albumArtist string
	}{
		{path: "/media/Movies/The.Matrix.1999.1080p.BluRay.x264.mkv", kind: KindMovie, title: "The Matrix", year: 1999},
		{path: "/media/Movies/Blade Runner 2049 (2017).mp4", kind: KindMovie, title: "Blade Runner 2049", year: 2017},
		{path: "/media/Movies/Heat (1995)/heat.mkv", kind: KindMovie, title: "Heat", year: 1995},
		{path: "/media/Movies/2001.mkv", kind: KindMovie, title: "2001"},
		{path: "/media/TV/Breaking.Bad.S01E02.Cats.in.the.Bag.720p.mkv", kind: KindEpisode, show: "Breaking Bad", season: 1, ep: 2, title: "Cats in the Bag"},
		{path: "/media/TV/The Office/Season 3/The Office - 3x05 - Initiation.mp4", kind: KindEpisode, show: "The Office", season: 3, ep: 5, title: "Initiation"},
		{path: "/media/TV/Firefly/Season 1/S01E01.mkv", kind: KindEpisode, show: "Firefly", season: 1, ep: 1, title: "Episode 1"},
		{path: "/media/Music/Pink Floyd/The Wall/03 Another Brick.flac", kind: KindMusic, title: "Another Brick", album: "The Wall", albumArtist: "Pink Floyd"},
		{path: "/media/Books/Dune 1965.epub", kind: KindBook, title: "Dune", year: 1965},
		{path: "/media/Comics/Saga/Saga 001.cbz", kind: KindComic, title: "Saga 001", album: "Saga"},
		{path: "/media/Photos/Trip/IMG_0001.jpg", kind: KindPhoto, title: "IMG_0001", album: "Trip"},
	}
	for _, c := range cases {
		it := ParseFile(c.path, lib, fakeInfo{size: 1})
		if it == nil {
			t.Errorf("%s: not parsed", c.path)
			continue
		}
		if it.Kind != c.kind || it.Title != c.title || it.Year != c.year || it.Show != c.show ||
			it.Season != c.season || it.Episode != c.ep || it.Album != c.album || it.AlbumArtist != c.albumArtist {
			t.Errorf("%s:\n got  kind=%q title=%q year=%d show=%q s%de%d album=%q aa=%q\n want kind=%q title=%q year=%d show=%q s%de%d album=%q aa=%q",
				c.path, it.Kind, it.Title, it.Year, it.Show, it.Season, it.Episode, it.Album, it.AlbumArtist,
				c.kind, c.title, c.year, c.show, c.season, c.ep, c.album, c.albumArtist)
		}
	}
}

func TestImportFolder(t *testing.T) {
	for in, want := range map[string]string{
		"Show.S02E03.mkv": "TV", "Movie (2000).mp4": "Movies", "a/b/song.mp3": "Music",
		"x.m4b": "Audiobooks", "x.pdf": "Books", "x.cbz": "Comics", "x.heic": "Photos", "notes.txt": "",
	} {
		if got := ImportFolder(in); got != want {
			t.Errorf("ImportFolder(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestArtworkIsNotAPhoto(t *testing.T) {
	for name, want := range map[string]bool{
		"Album/cover.jpg": false, "Album/Folder.PNG": false, "AlbumArt_{123}_Large.jpg": false, "poster.jpg": false,
		"Trip/IMG_0001.jpg": true, "covers-of-the-80s.jpg": true, "song.mp3": true,
	} {
		if got := Supported(name); got != want {
			t.Errorf("Supported(%q) = %v, want %v", name, got, want)
		}
	}
	for in, want := range map[string]string{"Band/Album/cover.jpg": "Music", "Heat (1995)/poster.jpg": "Movies"} {
		if got := ImportFolder(in); got != want {
			t.Errorf("ImportFolder(%q) = %q, want %q", in, got, want)
		}
	}
}
