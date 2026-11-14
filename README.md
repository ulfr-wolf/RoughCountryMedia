# Rough Country Media (RCM)

A light media server for your home network: movies, TV, music, audiobooks, books, comics and photos.
It's one small Go program with a built-in web UI. Open it from any browser on your network.

See [PLAN.md](PLAN.md) for the roadmap and what's done.

## Try it on this Mac
```bash
make run
```
Open http://localhost:8080, then drag media onto the page, or go to **Settings → Add folder** and enter a folder path.

Useful flags:
```
-addr :8080           where to listen
-data ./rcm-data      database location (default import folder is <data>/media)
-import /path         where drag-and-drop imports are saved
-lib /path            add a media folder (repeatable, remembered)
-rescan 6h            full rescan safety net (the file watcher catches most changes instantly)
```

## Deploy to the Mac Mini (Linux)
1. Build on this Mac:
   ```bash
   make linux
   ```
2. Copy `dist/rcm-linux-amd64` and `deploy/rcm.service` to the Mini, for example with `scp`.
3. On the Mini:
   ```bash
   sudo apt install ffmpeg
   sudo cp rcm-linux-amd64 /usr/local/bin/rcm
   sudo useradd --system --create-home --home-dir /var/lib/rcm rcm
   sudo cp rcm.service /etc/systemd/system/
   ```
   Edit `/etc/systemd/system/rcm.service` so `-lib` and `-import` point at your media drives, and make sure the `rcm` user can read them (and write to the import folder). Then:
   ```bash
   sudo systemctl daemon-reload && sudo systemctl enable --now rcm
   ```
4. Open `http://<mini-hostname>.local:8080` from any device on your network.

Logs: `journalctl -u rcm -f`. To update, rebuild, copy the new binary over, and run `sudo systemctl restart rcm`.

## Naming tips
- Movies: `The Matrix (1999).mkv` or `The.Matrix.1999.1080p.mkv`, or a `Heat (1995)/` folder
- TV: `Show Name/Season 1/Show.Name.S01E02.Title.mkv` (also `1x02`)
- Music: tags are read automatically; otherwise `Artist/Album/01 - Title.mp3`. Album art comes from the
  file's tags or a `cover.jpg`/`folder.jpg` in the album folder.
- Subtitles: put them next to the video with the same name, like `Heat (1995).srt` or `Heat (1995).en.srt`.
  Text subtitles inside MKV/MP4 files are found automatically.
- Photos: each folder becomes an album.

## Reading and listening
- **EPUB:** opens in a built-in reader with a contents menu, light/sepia/dark pages and text size. It remembers your place.
- **Comics (CBZ/CBR):** full-screen reader. Tap the right side or press → for the next page. It remembers your page.
- **PDF:** opens in the browser's own viewer. MOBI/AZW3 and CB7 can be downloaded.
- **Audiobooks (M4B):** chapter list, 0.75–2× speed, resume.

Thumbnails, extracted comics and subtitles are cached in `<data>/cache`. It's safe to delete; it rebuilds itself.

## What plays where
Files are streamed as-is (no transcoding yet). MP4 with H.264/AAC plays everywhere. MKV plays in
Chrome, Edge and Firefox when it uses H.264/VP9/AV1 video, but not in Safari. AVI/WMV need the
Download button and VLC until phase 4.

There's no login yet, so don't expose RCM to the internet.

## License
[MIT](LICENSE)
