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

## Install on the Mac Mini (Ubuntu Server)

**1. Set up the Mini.** Install **Ubuntu Server 26.04 LTS** (24.04 works too). During setup, tick
**Install OpenSSH server**, and use Ethernet (the Mini's Wi-Fi needs an extra driver).

**2. Make sure your media drives are mounted.** Ubuntu Server doesn't mount external drives on its own.
For each drive, find it with `lsblk -f`, then mount it at boot (example for a drive labeled `Media`):
```bash
sudo mkdir -p /media/media
echo 'LABEL=Media /media/media auto nofail,x-systemd.device-timeout=10s 0 2' | sudo tee -a /etc/fstab
sudo mount -a
```
`nofail` means the Mini still boots if the drive is unplugged. Drives formatted on a Mac as exFAT
work; for Mac-only formats (APFS/HFS+), reformat as ext4 or exFAT.

**3. Install RCM.** Pick one:
- **From this Mac (recommended):**
  ```bash
  ./deploy.sh you@mini.local
  ```
  It builds RCM, copies it over SSH and starts the guided installer. Use the Mini's IP address if `.local` doesn't resolve yet.
- **On the Mini, from GitHub** (needs a published release):
  ```bash
  curl -fsSL https://raw.githubusercontent.com/ulfr-wolf/RoughCountryMedia/main/install.sh | sudo bash
  ```
  It checks the download's SHA-256 checksum before running anything.

The installer shows your drives, asks for your media folders, the import folder and the port, installs
ffmpeg and avahi (so `mini.local` works), sets RCM up as a service that starts at boot, opens the
firewall if one is on, checks RCM is running, and prints the address to open. RCM runs as your user
account, so it can read the same files you can.

**Upgrading:** run the same command again; your settings are kept. To change folders or the port, add
`--reconfigure`. To remove RCM: `sudo rcm uninstall` (add `--purge` to delete its database and cache;
your media is never touched).

Logs: `journalctl -u rcm -f` · Status: `sudo systemctl status rcm`

### Publishing a release
Pushing a version tag builds Linux binaries and publishes a GitHub release (see `.github/workflows/release.yml`):
```bash
git tag v0.3.0 && git push origin v0.3.0
```

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
