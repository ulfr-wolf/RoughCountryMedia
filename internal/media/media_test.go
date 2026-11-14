package media

import (
	"archive/zip"
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func writeZip(t *testing.T, path string, files map[string]string, order []string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	for _, name := range order {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		w.Write([]byte(files[name]))
	}
	zw.Close()
	f.Close()
}

func TestNaturalLess(t *testing.T) {
	got := []string{"page10.jpg", "Page2.jpg", "page1.jpg", "page02b.jpg", "cover.jpg"}
	sortNatural(got)
	want := []string{"cover.jpg", "page1.jpg", "Page2.jpg", "page02b.jpg", "page10.jpg"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestLocalArchivePath(t *testing.T) {
	for in, want := range map[string]string{
		"pages/001.jpg": "pages/001.jpg", `pages\002.jpg`: "pages/002.jpg", "a/./b.jpg": "a/b.jpg",
		"../evil.jpg": "", "pages/../../evil.jpg": "", `..\evil.jpg`: "", "/etc/evil.jpg": "", "C:/evil.jpg": "", "": "",
	} {
		got, ok := localArchivePath(in)
		if (want == "") == ok || got != want {
			t.Errorf("localArchivePath(%q) = %q, %v; want %q", in, got, ok, want)
		}
	}
}

func TestToVTT(t *testing.T) {
	srt := "\xef\xbb\xbf1\r\n00:00:01,500 --> 00:00:03,000\r\nCaf\xe9 au lait\r\n"
	got := string(ToVTT([]byte(srt)))
	want := "WEBVTT\n\n1\n00:00:01.500 --> 00:00:03.000\nCafé au lait\n"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	vtt := "WEBVTT\n\n00:01.000 --> 00:02.000\nhi\n"
	if string(ToVTT([]byte(vtt))) != vtt {
		t.Error("VTT should pass through unchanged")
	}
}

func TestComicZipOrder(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "c.cbz")
	files := map[string]string{"p10.jpg": "x", "p2.jpg": "x", "p1.jpg": "x", "__MACOSX/._p1.jpg": "x", "notes.txt": "x", "sub/": ""}
	writeZip(t, p, files, []string{"p10.jpg", "p2.jpg", "notes.txt", "__MACOSX/._p1.jpg", "p1.jpg"})
	c, err := OpenComic(p, filepath.Join(dir, "extract"))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if want := []string{"p1.jpg", "p2.jpg", "p10.jpg"}; !reflect.DeepEqual(c.Pages, want) {
		t.Errorf("pages %v, want %v", c.Pages, want)
	}
	if _, name, err := ComicCover(p); err != nil || name != "p1.jpg" {
		t.Errorf("cover = %q, %v", name, err)
	}
}

const containerXML = `<?xml version="1.0"?><container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container">
<rootfiles><rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/></rootfiles></container>`

func TestEpubNav(t *testing.T) {
	p := filepath.Join(t.TempDir(), "b.epub")
	files := map[string]string{
		"mimetype":               "application/epub+zip",
		"META-INF/container.xml": containerXML,
		"OEBPS/content.opf": `<?xml version="1.0" encoding="UTF-8"?>
<package xmlns="http://www.idpf.org/2007/opf" version="3.0"><metadata xmlns:dc="http://purl.org/dc/elements/1.1/">
<dc:title> The Long Road </dc:title><dc:creator>A. Writer</dc:creator></metadata>
<manifest><item id="nav" href="nav.xhtml" media-type="application/xhtml+xml" properties="nav"/>
<item id="c1" href="Text/chapter%201.xhtml" media-type="application/xhtml+xml"/>
<item id="c2" href="Text/ch2.xhtml" media-type="application/xhtml+xml"/>
<item id="img" href="Images/front.jpg" media-type="image/jpeg" properties="cover-image"/></manifest>
<spine><itemref idref="c1"/><itemref idref="c2"/></spine></package>`,
		"OEBPS/nav.xhtml": `<html xmlns="http://www.w3.org/1999/xhtml" xmlns:epub="http://www.idpf.org/2007/ops"><body>
<nav epub:type="landmarks"><ol><li><a href="Text/ch2.xhtml">Not the TOC</a></li></ol></nav>
<nav epub:type="toc"><ol><li><a href="Text/chapter%201.xhtml">Part One&nbsp;</a>
<ol><li><a href="Text/ch2.xhtml#s1">  Chapter
 Two </a></li></ol></li></ol></nav></body></html>`,
		"OEBPS/Text/chapter 1.xhtml": "<html/>",
		"OEBPS/Text/ch2.xhtml":       "<html/>",
		"OEBPS/Images/front.jpg":     "jpg",
	}
	writeZip(t, p, files, []string{"mimetype", "META-INF/container.xml", "OEBPS/content.opf", "OEBPS/nav.xhtml",
		"OEBPS/Text/chapter 1.xhtml", "OEBPS/Text/ch2.xhtml", "OEBPS/Images/front.jpg"})
	e, err := OpenEpub(p)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	if e.Title != "The Long Road" || e.Author != "A. Writer" {
		t.Errorf("meta %q / %q", e.Title, e.Author)
	}
	if want := []string{"OEBPS/Text/chapter 1.xhtml", "OEBPS/Text/ch2.xhtml"}; !reflect.DeepEqual(e.Spine, want) {
		t.Errorf("spine %v", e.Spine)
	}
	if e.Cover != "OEBPS/Images/front.jpg" {
		t.Errorf("cover %q", e.Cover)
	}
	want := []TOCEntry{{"Part One", 0, 0}, {"Chapter Two", 1, 1}}
	if !reflect.DeepEqual(e.TOC, want) {
		t.Errorf("toc %+v, want %+v", e.TOC, want)
	}
}

func TestEpubNCX(t *testing.T) {
	p := filepath.Join(t.TempDir(), "b2.epub")
	files := map[string]string{
		"META-INF/container.xml": strings.Replace(containerXML, "OEBPS/content.opf", "content.opf", 1),
		"content.opf": `<package xmlns="http://www.idpf.org/2007/opf" version="2.0"><metadata>
<meta name="cover" content="cov"/></metadata>
<manifest><item id="ncx" href="toc.ncx" media-type="application/x-dtbncx+xml"/>
<item id="a" href="a.html" media-type="application/xhtml+xml"/><item id="cov" href="c.png" media-type="image/png"/></manifest>
<spine toc="ncx"><itemref idref="a"/></spine></package>`,
		"toc.ncx": `<ncx xmlns="http://www.daisy.org/z3986/2005/ncx/"><navMap>
<navPoint id="n1"><navLabel><text>Start</text></navLabel><content src="a.html"/>
<navPoint id="n2"><navLabel><text>Inner</text></navLabel><content src="a.html#x"/></navPoint></navPoint></navMap></ncx>`,
		"a.html": "<html/>",
		"c.png":  "png",
	}
	writeZip(t, p, files, []string{"META-INF/container.xml", "content.opf", "toc.ncx", "a.html", "c.png"})
	e, err := OpenEpub(p)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	if e.Cover != "c.png" {
		t.Errorf("cover %q", e.Cover)
	}
	if want := []TOCEntry{{"Start", 0, 0}, {"Inner", 0, 1}}; !reflect.DeepEqual(e.TOC, want) {
		t.Errorf("toc %+v", e.TOC)
	}
}

func TestFindSidecarSubtitles(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"Heat (1995).mkv", "Heat (1995).srt", "Heat (1995).en.forced.srt", "heat (1995)_es.vtt", "Heat 2.srt", "Other.srt"} {
		os.WriteFile(filepath.Join(dir, n), []byte("x"), 0o644)
	}
	subs := FindSubtitles(context.Background(), filepath.Join(dir, "Heat (1995).mkv"))
	got := map[string]string{}
	for _, s := range subs {
		if strings.HasPrefix(s.ID, "f") {
			got[s.Label] = s.Lang
		}
	}
	want := map[string]string{"Subtitles": "", "en.forced": "en", "es": "es"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}
