# Rough Country Media (RCM) — Plan

A light, self-hosted media server for the home network. Written in Go, it runs on a
2012 Mac Mini (i7, 8 GB RAM) running Linux and serves a web UI that any browser
on the LAN can open.

## Ground rules
- **Light:** one static binary, SQLite, no external services. It should idle well under 100 MB of RAM.
- **Direct play first:** files are streamed as they are, with HTTP range requests for seeking. There is no live transcoding unless phase 4 adds it.
- **Build on the Mac, run on the Mini:** `make linux` produces `dist/rcm-linux-amd64`.
- **Pure Go dependencies only** (modernc SQLite, fsnotify, dhowden/tag), so cross-compiling needs no C toolchain.

## Layout
```
cmd/rcm/            main: flags, startup, shutdown
internal/library/   SQLite store, file classification, scanner, watcher, ffprobe
internal/media/     inside files: comics, EPUBs, thumbnails, subtitles, chapters
internal/server/    HTTP API, streaming, drag-and-drop import, readers
web/                embedded single-page UI (HTML/CSS/JS, no build step)
deploy/             systemd service for the Mini
```

## Media kinds
| Kind | Extensions | Grouping |
|---|---|---|
| movie | mp4 m4v mkv avi mov webm wmv mpg ts (no SxxEyy) | title, year |
| episode | same video extensions, name has S01E02 or 1x02 | show → season → episode |
| music | mp3 flac m4a aac ogg opus wav wma | album artist → album → track (tags, then folders) |
| audiobook | m4b | author, title |
| book | epub pdf mobi azw3 | title |
| comic | cbz cbr cb7 | title |
| photo | jpg jpeg png gif webp heic | folder |

## Phases
### Phase 1 — Core ✅ (session 1)
- [x] Library folders stored in the DB, added at startup with `-lib` or from the Settings page
- [x] Full scan that skips unchanged files (size + mtime) and removes deleted ones; nothing is deleted if a drive is unplugged
- [x] File watcher (fsnotify) with debounce, so new files appear automatically
- [x] Background ffprobe pass for durations, one file at a time to spare the Mini
- [x] API: items, shows, albums, stats, continue watching, progress, libraries, rescan
- [x] Range-request streaming
- [x] Drag-and-drop import (files and whole folders) streamed straight to disk and sorted into Movies/TV/Music/Books/...
- [x] Web UI: Home, Movies, TV, Music, Books, Comics, Photos, Settings; video player with resume; music bar with queue
- [x] Linux build target and systemd unit

### Phase 2 — All media, properly ✅ (session 2)
- [x] In-browser EPUB reader. Instead of vendoring epub.js, the server parses the EPUB in Go and serves
      its chapters into a sandboxed iframe with a script-blocking CSP: no JS dependency, works offline.
      Contents menu from the EPUB3 nav or EPUB2 NCX, light/sepia/dark, text size, resume by chapter and scroll position.
      Title and author come from the book's metadata when scanning.
- [x] Comic reader: CBZ read page by page from the zip; CBR extracted once with pure-Go rardecode
      (cached, last 8 kept). Detected by file signature, not extension. Fit page/width, slider, keys, resume.
      CB7 still downloads only.
- [x] Audiobook chapters (ffprobe) with a clickable list, playback speed (remembered), resume
- [x] Thumbnails via ffmpeg, cached in `<data>/cache/thumbs`, max 2 at a time: video frames, album art
      (embedded or cover.jpg/folder.jpg), comic first page, EPUB cover, photo previews
- [x] Photos grouped into albums by folder, lightbox with prev/next and arrow keys; HEIC falls back to the thumbnail
- [x] Subtitles: sidecar .srt/.vtt (Movie.srt, Movie.en.srt; Windows-1252 fixed) and text tracks
      embedded in MKV/MP4 (extracted once, cached). Image subtitles (PGS/VobSub) aren't supported.
- [x] "Pick up where you left off" covers books and comics too
- [x] Album art and posters (cover.jpg, folder.jpg, poster.jpg...) are no longer indexed as photos

### Phase 3 — Polish
- [ ] Posters and metadata from TMDB (movies/TV) and MusicBrainz/Cover Art Archive (optional API key)
- [ ] Manual "fix match" and edit metadata
- [ ] Profiles (per-person progress), simple PIN
- [ ] Better search (FTS5), sorting and filters
- [ ] Watched/unwatched, "next episode" button

### Phase 4 — Optional and heavy
- [ ] On-demand transcoding with ffmpeg (HLS) for files the browser can't play
- [ ] Remote access over Tailscale (recommended instead of exposing ports)

## Known limits
- No login: anyone on the LAN can use it, so don't port-forward it.
- Browsers can't play every codec. MP4/H.264/AAC plays everywhere. MKV plays in Chrome/Edge/Firefox when its codecs are H.264/VP9/AV1 with AAC/Opus, but not in Safari. AVI/WMV need phase 4 transcoding.
- MOBI/AZW3 and CB7 download only. PDFs use the browser's own viewer.
- Posters on movie cards are video frames until phase 3 adds real posters.
- The CBR (RAR) path is covered by the library's own tests but hasn't been tried with a real .cbr here
  (no RAR tool on this Mac to make one).

## Session log
- **Session 1 (2026-11-13):** project created; phase 1 built and tested on the Mac with generated
  sample media (`testdata/sample`, gitignored). Verified: scanning and incremental rescans, watcher
  add/remove, ffprobe durations, range streaming (206), import sorting and path-traversal guard,
  video and music playback in the browser, Settings. Idle RAM ~17 MB. Linux binary builds; **not
  yet run on the Mini.**
- **Session 2 (2026-11-13):** phase 2 built and tested in the browser with generated samples (EPUB with
  nav TOC and an embedded `<script>` to prove the sandbox, 12-page CBZ, M4B with chapters, MKV with an
  embedded subtitle and a Windows-1252 sidecar SRT, album cover.jpg). Fixed along the way: collapsed
  audio controls in the player, and album art being indexed as photos. Idle RAM ~19 MB.
- **Next session:** deploy to the Mini (still not done) and fix anything that turns up, then phase 3.
