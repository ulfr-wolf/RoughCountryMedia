package media

import (
	"archive/zip"
	"encoding/xml"
	"errors"
	"io"
	"net/url"
	"path"
	"strings"
)

// Epub is an open EPUB file: its reading order, table of contents and cover.
type Epub struct {
	zr     *zip.ReadCloser
	files  map[string]*zip.File
	Title  string
	Author string
	Spine  []string   // archive paths in reading order
	TOC    []TOCEntry // may be empty
	Cover  string     // archive path of the cover image, if known
}

type TOCEntry struct {
	Title string `json:"title"`
	Spine int    `json:"spine"`
	Depth int    `json:"depth"`
}

type container struct {
	Rootfiles []struct {
		FullPath string `xml:"full-path,attr"`
	} `xml:"rootfiles>rootfile"`
}

type opfItem struct {
	ID         string `xml:"id,attr"`
	Href       string `xml:"href,attr"`
	MediaType  string `xml:"media-type,attr"`
	Properties string `xml:"properties,attr"`
}

type opfDoc struct {
	Metadata struct {
		Titles   []string `xml:"title"`
		Creators []string `xml:"creator"`
		Metas    []struct {
			Name    string `xml:"name,attr"`
			Content string `xml:"content,attr"`
		} `xml:"meta"`
	} `xml:"metadata"`
	Items []opfItem `xml:"manifest>item"`
	Spine struct {
		Toc   string `xml:"toc,attr"`
		Items []struct {
			IDRef string `xml:"idref,attr"`
		} `xml:"itemref"`
	} `xml:"spine"`
}

type navPoint struct {
	Label   string `xml:"navLabel>text"`
	Content struct {
		Src string `xml:"src,attr"`
	} `xml:"content"`
	Children []navPoint `xml:"navPoint"`
}

type ncxDoc struct {
	Points []navPoint `xml:"navMap>navPoint"`
}

var ErrNotEpub = errors.New("not a valid EPUB")

// newDecoder is lenient: real-world EPUBs contain HTML entities, odd encodings and sloppy markup.
func newDecoder(r io.Reader) *xml.Decoder {
	d := xml.NewDecoder(r)
	d.Strict = false
	d.AutoClose = xml.HTMLAutoClose
	d.Entity = xml.HTMLEntity
	d.CharsetReader = func(_ string, in io.Reader) (io.Reader, error) { return in, nil }
	return d
}

// resolve turns an href relative to the file at base into an archive path (no #fragment).
func resolve(baseFile, href string) string {
	href, _, _ = strings.Cut(href, "#")
	if u, err := url.PathUnescape(href); err == nil {
		href = u
	}
	return path.Clean(path.Join(path.Dir(baseFile), href))
}

func OpenEpub(p string) (*Epub, error) {
	zr, err := zip.OpenReader(p)
	if err != nil {
		return nil, err
	}
	e := &Epub{zr: zr, files: map[string]*zip.File{}}
	for _, f := range zr.File {
		e.files[f.Name] = f
	}
	if err := e.parse(); err != nil {
		zr.Close()
		return nil, err
	}
	return e, nil
}

func (e *Epub) Close() { e.zr.Close() }

func (e *Epub) readXML(name string, v any) error {
	f, ok := e.files[name]
	if !ok {
		return ErrNotEpub
	}
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	return newDecoder(io.LimitReader(rc, 8<<20)).Decode(v)
}

func (e *Epub) parse() error {
	var c container
	if err := e.readXML("META-INF/container.xml", &c); err != nil || len(c.Rootfiles) == 0 {
		return ErrNotEpub
	}
	opfPath := c.Rootfiles[0].FullPath
	var o opfDoc
	if err := e.readXML(opfPath, &o); err != nil {
		return ErrNotEpub
	}
	for _, t := range o.Metadata.Titles {
		if t = strings.TrimSpace(t); t != "" {
			e.Title = t
			break
		}
	}
	for _, a := range o.Metadata.Creators {
		if a = strings.TrimSpace(a); a != "" {
			e.Author = a
			break
		}
	}
	byID := map[string]opfItem{}
	for _, it := range o.Items {
		byID[it.ID] = it
	}
	for _, ref := range o.Spine.Items {
		if it, ok := byID[ref.IDRef]; ok {
			e.Spine = append(e.Spine, resolve(opfPath, it.Href))
		}
	}
	if len(e.Spine) == 0 {
		return ErrNotEpub
	}

	// Cover: EPUB3 property, then EPUB2 <meta name="cover">, then any image named like a cover.
	for _, it := range o.Items {
		if hasWord(it.Properties, "cover-image") {
			e.Cover = resolve(opfPath, it.Href)
		}
	}
	if e.Cover == "" {
		for _, m := range o.Metadata.Metas {
			if m.Name == "cover" {
				if it, ok := byID[m.Content]; ok && strings.HasPrefix(it.MediaType, "image/") {
					e.Cover = resolve(opfPath, it.Href)
				}
			}
		}
	}
	if e.Cover == "" {
		for _, it := range o.Items {
			if strings.HasPrefix(it.MediaType, "image/") && strings.Contains(strings.ToLower(it.ID+it.Href), "cover") {
				e.Cover = resolve(opfPath, it.Href)
				break
			}
		}
	}

	// Table of contents: EPUB3 nav document, else the EPUB2 NCX.
	for _, it := range o.Items {
		if hasWord(it.Properties, "nav") {
			e.parseNav(resolve(opfPath, it.Href))
			break
		}
	}
	if len(e.TOC) == 0 {
		ncx := byID[o.Spine.Toc]
		if ncx.Href == "" {
			for _, it := range o.Items {
				if it.MediaType == "application/x-dtbncx+xml" {
					ncx = it
				}
			}
		}
		if ncx.Href != "" {
			e.parseNCX(resolve(opfPath, ncx.Href))
		}
	}
	return nil
}

func hasWord(list, word string) bool {
	for _, w := range strings.Fields(list) {
		if w == word {
			return true
		}
	}
	return false
}

func (e *Epub) spineIndex(p string) int {
	for i, s := range e.Spine {
		if s == p {
			return i
		}
	}
	return -1
}

func (e *Epub) addTOC(title, target string, depth int) {
	title = strings.Join(strings.Fields(title), " ")
	if i := e.spineIndex(target); i >= 0 && title != "" {
		e.TOC = append(e.TOC, TOCEntry{Title: title, Spine: i, Depth: depth})
	}
}

func (e *Epub) parseNCX(name string) {
	var doc ncxDoc
	if e.readXML(name, &doc) != nil {
		return
	}
	var walk func(points []navPoint, depth int)
	walk = func(points []navPoint, depth int) {
		for _, p := range points {
			e.addTOC(p.Label, resolve(name, p.Content.Src), depth)
			walk(p.Children, depth+1)
		}
	}
	walk(doc.Points, 0)
}

// parseNav reads <nav epub:type="toc"> links from an EPUB3 navigation document.
func (e *Epub) parseNav(name string) {
	f, ok := e.files[name]
	if !ok {
		return
	}
	rc, err := f.Open()
	if err != nil {
		return
	}
	defer rc.Close()
	d := newDecoder(io.LimitReader(rc, 8<<20))
	navLevel, tocLevel, olDepth := 0, 0, 0
	inLink, href, text := false, "", ""
	for {
		tok, err := d.Token()
		if err != nil {
			return
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "nav":
				navLevel++
				for _, a := range t.Attr {
					if a.Name.Local == "type" && hasWord(a.Value, "toc") && tocLevel == 0 {
						tocLevel = navLevel
					}
				}
			case "ol":
				if tocLevel > 0 {
					olDepth++
				}
			case "a":
				if tocLevel > 0 {
					inLink, href, text = true, "", ""
					for _, a := range t.Attr {
						if a.Name.Local == "href" {
							href = a.Value
						}
					}
				}
			}
		case xml.CharData:
			if inLink {
				text += string(t)
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "a":
				if inLink && href != "" {
					e.addTOC(text, resolve(name, href), max(olDepth-1, 0))
				}
				inLink = false
			case "ol":
				if tocLevel > 0 {
					olDepth--
				}
			case "nav":
				if navLevel == tocLevel {
					return
				}
				navLevel--
			}
		}
	}
}

// Open returns a file from inside the EPUB.
func (e *Epub) Open(name string) (io.ReadCloser, error) {
	f, ok := e.files[name]
	if !ok {
		return nil, ErrNotEpub
	}
	return f.Open()
}

// EpubMeta returns an EPUB's title and author without keeping it open.
func EpubMeta(p string) (title, author string) {
	e, err := OpenEpub(p)
	if err != nil {
		return "", ""
	}
	defer e.Close()
	return e.Title, e.Author
}
