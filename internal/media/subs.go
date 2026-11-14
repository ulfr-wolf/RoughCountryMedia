package media

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/text/encoding/charmap"
)

// Subtitle is a text subtitle track: a sidecar .srt/.vtt file or a stream inside the video.
type Subtitle struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Lang  string `json:"lang,omitempty"`
	file  string // sidecar path
	index int    // embedded stream index
}

// textSubCodecs are subtitle formats ffmpeg can turn into WebVTT (image-based PGS/VobSub can't be).
var textSubCodecs = map[string]bool{"subrip": true, "srt": true, "ass": true, "ssa": true, "mov_text": true, "webvtt": true, "text": true}

var reLang = regexp.MustCompile(`^[a-z]{2,3}$`)

// FindSubtitles lists sidecar files named like the video (Movie.srt, Movie.en.srt,
// Movie.en.forced.vtt) and text subtitle streams inside it.
func FindSubtitles(ctx context.Context, video string) []Subtitle {
	out := []Subtitle{}
	base := strings.ToLower(strings.TrimSuffix(filepath.Base(video), filepath.Ext(video)))
	if entries, err := os.ReadDir(filepath.Dir(video)); err == nil {
		n := 0
		for _, e := range entries {
			name := strings.ToLower(e.Name())
			ext := filepath.Ext(name)
			if e.IsDir() || (ext != ".srt" && ext != ".vtt") {
				continue
			}
			stem := strings.TrimSuffix(name, ext)
			if stem != base && !strings.HasPrefix(stem, base+".") && !strings.HasPrefix(stem, base+"_") && !strings.HasPrefix(stem, base+" ") {
				continue
			}
			origStem := e.Name()[:len(e.Name())-len(ext)]
			extra := ""
			if len(origStem) >= len(base) { // lowercasing can change byte length for some Unicode names
				extra = strings.Trim(origStem[len(base):], "._- ")
			}
			s := Subtitle{ID: fmt.Sprintf("f%d", n), Label: extra, file: filepath.Join(filepath.Dir(video), e.Name())}
			if first, _, _ := strings.Cut(strings.ToLower(extra), "."); reLang.MatchString(first) {
				s.Lang = first
			}
			if s.Label == "" {
				s.Label = "Subtitles"
			}
			out = append(out, s)
			n++
		}
	}
	_, ffprobe := tools()
	if ffprobe == "" {
		return out
	}
	raw, err := run(ctx, 20*time.Second, ffprobe, "-v", "error", "-select_streams", "s",
		"-show_entries", "stream=index,codec_name:stream_tags=language,title", "-of", "json", video)
	if err != nil {
		return out
	}
	var probe struct {
		Streams []struct {
			Index int    `json:"index"`
			Codec string `json:"codec_name"`
			Tags  struct {
				Language string `json:"language"`
				Title    string `json:"title"`
			} `json:"tags"`
		} `json:"streams"`
	}
	if json.Unmarshal(raw, &probe) != nil {
		return out
	}
	for _, st := range probe.Streams {
		if !textSubCodecs[st.Codec] {
			continue
		}
		label := st.Tags.Title
		if label == "" {
			label = st.Tags.Language
		}
		if label == "" {
			label = fmt.Sprintf("Track %d", st.Index)
		}
		lang := st.Tags.Language
		if lang == "und" {
			lang = ""
		}
		out = append(out, Subtitle{ID: fmt.Sprintf("e%d", st.Index), Label: label + " (embedded)", Lang: lang, index: st.Index})
	}
	return out
}

// SubtitleVTT returns subtitle id of the video as WebVTT. Embedded tracks are
// extracted once with ffmpeg (it must read the whole file) and cached in cacheDir.
func SubtitleVTT(ctx context.Context, video, id, cacheKey, cacheDir string) ([]byte, error) {
	var sub *Subtitle
	for _, s := range FindSubtitles(ctx, video) {
		if s.ID == id {
			sub = &s
			break
		}
	}
	if sub == nil {
		return nil, os.ErrNotExist
	}
	if sub.file != "" {
		data, err := os.ReadFile(sub.file)
		if err != nil {
			return nil, err
		}
		return ToVTT(data), nil
	}
	cached := filepath.Join(cacheDir, fmt.Sprintf("%s-%s.vtt", cacheKey, id))
	if data, err := os.ReadFile(cached); err == nil {
		return data, nil
	}
	ffmpeg, _ := tools()
	if ffmpeg == "" {
		return nil, errors.New("ffmpeg is needed for embedded subtitles")
	}
	os.MkdirAll(cacheDir, 0o755)
	tmp := cached + ".tmp"
	_, err := run(context.Background(), 10*time.Minute, ffmpeg, "-v", "error", "-y", "-i", video,
		"-map", "0:"+strconv.Itoa(sub.index), "-f", "webvtt", tmp)
	if err != nil {
		os.Remove(tmp)
		return nil, fmt.Errorf("extract subtitles: %w", err)
	}
	if err := os.Rename(tmp, cached); err != nil {
		return nil, err
	}
	return os.ReadFile(cached)
}

var reSRTTime = regexp.MustCompile(`(\d{1,2}:\d{2}:\d{2}),(\d{3})`)

// ToVTT converts SRT (or passes through VTT), fixing the BOM, line endings and
// the Windows-1252 encoding that many older .srt files use.
func ToVTT(data []byte) []byte {
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	if !utf8.Valid(data) {
		if dec, err := charmap.Windows1252.NewDecoder().Bytes(data); err == nil {
			data = dec
		}
	}
	s := strings.ReplaceAll(string(data), "\r\n", "\n")
	if strings.HasPrefix(s, "WEBVTT") {
		return []byte(s)
	}
	return []byte("WEBVTT\n\n" + reSRTTime.ReplaceAllString(s, "$1.$2"))
}

// Chapter is a chapter mark in an audiobook or video.
type Chapter struct {
	Start float64 `json:"start"`
	End   float64 `json:"end"`
	Title string  `json:"title"`
}

func Chapters(ctx context.Context, file string) ([]Chapter, error) {
	_, ffprobe := tools()
	out := []Chapter{}
	if ffprobe == "" {
		return out, nil
	}
	raw, err := run(ctx, 30*time.Second, ffprobe, "-v", "error", "-print_format", "json", "-show_chapters", file)
	if err != nil {
		return out, err
	}
	var probe struct {
		Chapters []struct {
			Start string `json:"start_time"`
			End   string `json:"end_time"`
			Tags  struct {
				Title string `json:"title"`
			} `json:"tags"`
		} `json:"chapters"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return out, err
	}
	for i, c := range probe.Chapters {
		start, _ := strconv.ParseFloat(c.Start, 64)
		end, _ := strconv.ParseFloat(c.End, 64)
		title := strings.TrimSpace(c.Tags.Title)
		if title == "" {
			title = fmt.Sprintf("Chapter %d", i+1)
		}
		out = append(out, Chapter{Start: start, End: end, Title: title})
	}
	return out, nil
}

func readLimited(r io.Reader, n int64) ([]byte, error) { return io.ReadAll(io.LimitReader(r, n)) }
