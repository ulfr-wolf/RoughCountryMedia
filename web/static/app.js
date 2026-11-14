// Rough Country Media — browser UI. Plain JS, no build step.
"use strict";

const $ = (sel, el = document) => el.querySelector(sel);
const view = $("#view");

// ---------- helpers ----------
const esc = (s) => String(s ?? "").replace(/[&<>"']/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));

async function api(path, opts = {}) {
  const res = await fetch(path, opts);
  if (res.status === 204) return null;
  const data = await res.json().catch(() => null);
  if (!res.ok) throw new Error((data && data.error) || res.statusText);
  return data;
}

function fmtTime(sec) {
  sec = Math.floor(sec || 0);
  const h = Math.floor(sec / 3600), m = Math.floor((sec % 3600) / 60), s = sec % 60;
  return h ? `${h}:${String(m).padStart(2, "0")}:${String(s).padStart(2, "0")}` : `${m}:${String(s).padStart(2, "0")}`;
}
function fmtSize(b) {
  if (b > 1e9) return (b / 1e9).toFixed(1) + " GB";
  if (b > 1e6) return (b / 1e6).toFixed(0) + " MB";
  return Math.max(1, Math.round(b / 1e3)) + " KB";
}
function hue(str) {
  let h = 0;
  for (const c of String(str)) h = (h * 31 + c.charCodeAt(0)) >>> 0;
  return h % 360;
}
function artStyle(key) {
  const h = hue(key);
  return `background: linear-gradient(140deg, hsl(${h} 50% 38%), hsl(${(h + 45) % 360} 55% 20%))`;
}
let toastTimer;
function toast(msg) {
  const t = $("#toast");
  t.textContent = msg;
  t.hidden = false;
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => (t.hidden = true), 3500);
}
function localGet(k) { try { return localStorage.getItem(k); } catch { return null; } }
function localSet(k, v) { try { localStorage.setItem(k, v); } catch {} }

const streamURL = (it) => `/api/items/${it.id}/stream`;
const thumbURL = (id) => `/api/items/${id}/thumb`;
// The gradient and title stay underneath; a thumbnail covers them when one exists.
const thumbImg = (id) => (id ? `<img loading="lazy" src="${thumbURL(id)}" alt="" onerror="this.remove()">` : "");
const KIND_ICON = { movie: "🎬", episode: "📺", music: "🎵", audiobook: "🎧", book: "📖", comic: "💬", photo: "🖼" };
const pct = (it) => (it.duration && it.position ? Math.min(100, (it.position / it.duration) * 100) : 0);

// ---------- cards ----------
function itemSub(it) {
  switch (it.kind) {
    case "movie": return it.year || "";
    case "episode": return `${it.show} · S${String(it.season).padStart(2, "0")}E${String(it.episode).padStart(2, "0")}`;
    case "music": case "audiobook": case "book": return it.artist || it.album || (it.year || "");
    case "photo": return it.album || "";
    default: return it.year || it.ext.toUpperCase();
  }
}
function itemCard(it, shape = "") {
  const label = it.kind === "episode" ? it.show : it.title;
  const hasThumb = !(it.kind === "book" && it.ext !== "epub");
  return `<button class="card" data-id="${it.id}">
    <div class="art ${shape}" style="${artStyle(label)}">${esc(label)}${hasThumb ? thumbImg(it.id) : ""}
      <span class="kind-icon">${KIND_ICON[it.kind] || ""}</span>
      ${pct(it) ? `<span class="bar" style="width:${pct(it)}%"></span>` : ""}
    </div>
    <div class="t">${esc(it.title)}</div>
    <div class="s">${esc(itemSub(it))}</div>
  </button>`;
}
function linkCard(href, title, sub, shape = "", coverId = 0) {
  return `<a class="card" href="${href}">
    <div class="art ${shape}" style="${artStyle(title)}">${esc(title)}${thumbImg(coverId)}</div>
    <div class="t">${esc(title)}</div><div class="s">${esc(sub)}</div></a>`;
}
// clicking a card with data-id opens that item; cache items by id per render
let shown = new Map();
function remember(items) { for (const it of items) shown.set(it.id, it); return items; }
view.addEventListener("click", (e) => {
  const card = e.target.closest("[data-id]");
  if (!card) return;
  const it = shown.get(Number(card.dataset.id));
  if (it) openItem(it, card);
});

function emptyState(title, body) {
  return `<div class="empty"><h2>${esc(title)}</h2><p>${body}</p>
    <p><button class="btn primary" onclick="document.getElementById('add-btn').click()">+ Add media</button>
    <a class="btn" href="#/settings">Add a folder on this computer</a></p></div>`;
}

// ---------- pages ----------
const pages = {
  async home() {
    const [stats, cont, recent] = await Promise.all([
      api("/api/stats"), api("/api/continue"),
      api("/api/items?sort=added&limit=24&kind=movie,episode,music,audiobook,book,comic"),
    ]);
    remember(cont); remember(recent);
    const total = Object.values(stats).reduce((a, b) => a + b, 0);
    if (!total) {
      return `<h1>Welcome to Rough Country Media</h1>` +
        emptyState("Your library is empty", "Drag movies, shows, music, books, comics or photos onto this page, or add a folder from your drives in Settings.");
    }
    const chip = (k, label, href) => stats[k] ? `<a class="stat" href="${href}"><b>${stats[k]}</b>${label}</a>` : "";
    const shape = (i) => (i.kind === "music" || i.kind === "audiobook" ? "square" : "");
    return `<div class="page-head"><h1>Home</h1></div>
      <div class="stats">${chip("movie", "Movies", "#/movies")}${chip("episode", "Episodes", "#/tv")}
        ${chip("music", "Songs", "#/music")}${chip("audiobook", "Audiobooks", "#/audiobooks")}
        ${chip("book", "Books", "#/books")}${chip("comic", "Comics", "#/comics")}${chip("photo", "Photos", "#/photos")}</div>
      ${cont.length ? `<h2>Pick up where you left off</h2><div class="row">${cont.map((i) => itemCard(i, shape(i))).join("")}</div>` : ""}
      <h2>Recently added</h2><div class="row">${recent.map((i) => itemCard(i, shape(i))).join("")}</div>`;
  },

  async movies() { return gridPage("Movies", "movie", "Drop movie files here, like “The Matrix (1999).mkv”."); },
  async books() { return gridPage("Books", "book", "EPUB and PDF open right in the browser; MOBI and AZW3 can be downloaded."); },
  async comics() { return gridPage("Comics", "comic", "CBZ and CBR comics open in the built-in reader."); },
  async audiobooks() { return gridPage("Audiobooks", "audiobook", "M4B audiobook files show up here, with chapters and playback speed.", "square"); },

  async photos() {
    const items = remember(await api("/api/items?kind=photo&sort=added"));
    if (!items.length) return `<h1>Photos</h1>` + emptyState("No photos yet", "JPG, PNG, GIF, WebP and HEIC files are supported. Each folder becomes an album.");
    const albums = new Map();
    for (const it of items) {
      const k = it.album || "Unsorted";
      if (!albums.has(k)) albums.set(k, []);
      albums.get(k).push(it);
    }
    return `<div class="page-head"><h1>Photos</h1><span class="muted">${items.length} photos · ${albums.size} album${albums.size > 1 ? "s" : ""}</span></div>` +
      [...albums].map(([name, list]) => `<h2>${esc(name)} <span class="muted small">${list.length}</span></h2>
        <div class="grid photos">${list.map((i) => itemCard(i, "square")).join("")}</div>`).join("");
  },

  async tv(show) {
    if (show) return showPage(show);
    const shows = await api("/api/shows");
    if (!shows.length) return `<h1>TV</h1>` + emptyState("No TV shows yet", "Episodes are detected from names like “Show.Name.S01E02.mkv” or “Show - 1x02.mp4”.");
    return `<div class="page-head"><h1>TV</h1><span class="muted">${shows.length} shows</span></div>
      <div class="grid">${shows.map((s) => linkCard(`#/tv/${encodeURIComponent(s.show)}`, s.show,
        `${s.seasons} season${s.seasons > 1 ? "s" : ""} · ${s.episodes} ep`, "", s.cover_id)).join("")}</div>`;
  },

  async music(artist, album) {
    if (album !== undefined) return albumPage(artist, album);
    const albums = await api("/api/albums");
    if (!albums.length) return `<h1>Music</h1>` + emptyState("No music yet", "MP3, FLAC, M4A, OGG, Opus and WAV files are supported. Tags and album art are read automatically.");
    return `<div class="page-head"><h1>Music</h1><span class="muted">${albums.length} albums</span></div>
      <div class="grid">${albums.map((a) => linkCard(`#/music/${encodeURIComponent(a.album_artist)}/${encodeURIComponent(a.album)}`,
        a.album, `${a.album_artist}${a.year ? " · " + a.year : ""}`, "square", a.cover_id)).join("")}</div>`;
  },

  async search(q) {
    const items = remember(await api(`/api/items?q=${encodeURIComponent(q)}&limit=300`));
    if (!items.length) return `<h1>Search</h1><p class="muted">Nothing matches “${esc(q)}”.</p>`;
    const groups = {};
    for (const it of items) (groups[it.kind] ||= []).push(it);
    const names = { movie: "Movies", episode: "TV episodes", music: "Songs", audiobook: "Audiobooks", book: "Books", comic: "Comics", photo: "Photos" };
    return `<h1>Results for “${esc(q)}”</h1>` + Object.entries(groups).map(([k, list]) =>
      `<h2>${names[k] || k}</h2><div class="grid">${list.map((i) => itemCard(i, ["music", "photo", "audiobook"].includes(k) ? "square" : "")).join("")}</div>`).join("");
  },

  async settings() {
    const [libs, st] = await Promise.all([api("/api/libraries"), api("/api/status")]);
    return `<h1>Settings</h1>
      <section class="panel"><h2>Library folders</h2>
        <p class="muted small">RCM scans these folders and watches them for new files. Drag-and-drop imports are saved in the import folder, sorted into Movies, TV, Music and so on.</p>
        ${libs.map((l) => `<div class="lib"><code>${esc(l.path)}</code>
          ${l.import ? `<span class="pill">import folder</span>` : ""}
          <span class="pill ${l.online ? "on" : "off"}">${l.online ? "online" : "offline"}</span>
          ${l.import ? "" : `<button class="btn small danger" data-remove="${l.id}">Remove</button>`}</div>`).join("")}
        <form class="form-row" id="add-lib">
          <input name="path" placeholder="/Volumes/Media/Movies  or  /mnt/media" required aria-label="Folder path">
          <button class="btn primary">Add folder</button>
        </form>
        <p class="muted small">Type the folder's full path as the server sees it. Removing a folder only removes it from RCM; your files are not touched.</p>
      </section>
      <section class="panel"><h2>Scanning</h2>
        <p>${st.scanning ? "⏳ Scanning now…" : `Last scan: ${st.last_scan ? new Date(st.last_scan * 1000).toLocaleString() : "not yet"}`}
          · ${st.total_items} items · watcher ${st.watching ? "on" : "off"} · ffprobe ${st.ffprobe ? "found" : "<b>missing</b> (install ffmpeg for durations, thumbnails and chapters)"}</p>
        <button class="btn" id="rescan">Rescan all folders</button>
      </section>`;
  },
};

async function gridPage(title, kind, hint, shape = "") {
  const sort = localGet("sort:" + kind) || "title";
  const items = remember(await api(`/api/items?kind=${kind}&sort=${sort}`));
  if (!items.length) return `<h1>${title}</h1>` + emptyState(`No ${title.toLowerCase()} yet`, esc(hint));
  return `<div class="page-head"><h1>${title}</h1>
      <label class="muted small">Sort <select data-sort="${kind}">
        ${[["title", "Title"], ["added", "Recently added"], ["year", "Year"]].map(([v, l]) => `<option value="${v}" ${v === sort ? "selected" : ""}>${l}</option>`).join("")}
      </select></label></div>
    <div class="grid">${items.map((i) => itemCard(i, shape)).join("")}</div>`;
}

async function showPage(show) {
  const eps = remember(await api(`/api/items?show=${encodeURIComponent(show)}&kind=episode`));
  const seasons = {};
  for (const e of eps) (seasons[e.season] ||= []).push(e);
  return `<a class="crumb" href="#/tv">← TV</a><div class="page-head"><h1>${esc(show)}</h1><span class="muted">${eps.length} episodes</span></div>` +
    Object.entries(seasons).map(([s, list]) => `<h2>${Number(s) === 0 ? "Specials" : "Season " + s}</h2>
      <ul class="list">${list.map((e) => `<li data-id="${e.id}"><span class="n">${e.episode}</span>
        <span class="grow">${esc(e.title)}</span>
        <span class="d">${e.position && pct(e) > 95 ? "✓ " : e.position ? fmtTime(e.position) + " / " : ""}${e.duration ? fmtTime(e.duration) : ""}</span></li>`).join("")}</ul>`).join("");
}

async function albumPage(artist, album) {
  const tracks = remember(await api(`/api/items?kind=music&album_artist=${encodeURIComponent(artist)}&album=${encodeURIComponent(album)}`));
  const total = tracks.reduce((a, t) => a + (t.duration || 0), 0);
  view.dataset.queue = JSON.stringify(tracks.map((t) => t.id));
  const cover = tracks.length ? Math.min(...tracks.map((t) => t.id)) : 0;
  return `<a class="crumb" href="#/music">← Music</a>
    <div class="album-head">
      <div class="art square" style="${artStyle(album)}">${esc(album)}${thumbImg(cover)}</div>
      <div><h1>${esc(album)}</h1><p class="muted">${esc(artist)} · ${tracks.length} tracks${total ? " · " + fmtTime(total) : ""}</p>
        <button class="btn primary" id="play-all">▶ Play album</button></div>
    </div>
    <ul class="list" id="tracks">${tracks.map((t, i) => `<li data-track="${i}" data-tid="${t.id}"><span class="n">${t.track || i + 1}</span>
      <span class="grow">${esc(t.title)}${t.artist && t.artist !== artist ? ` <span class="muted">— ${esc(t.artist)}</span>` : ""}</span>
      <span class="d">${t.duration ? fmtTime(t.duration) : ""}</span></li>`).join("")}</ul>`;
}

// ---------- router ----------
async function route() {
  const parts = (location.hash.replace(/^#\/?/, "") || "home").split("/").map(decodeURIComponent);
  const [name, ...args] = parts;
  const page = pages[name] || pages.home;
  document.querySelectorAll(".tabs a").forEach((a) => a.classList.toggle("active", a.dataset.tab === name));
  shown = new Map();
  try {
    view.innerHTML = await page(...args);
  } catch (err) {
    view.innerHTML = `<div class="empty"><h2>Something went wrong</h2><p>${esc(err.message)}</p></div>`;
  }
  markPlaying();
}
window.addEventListener("hashchange", route);

// page-level interactions
view.addEventListener("change", (e) => {
  const sel = e.target.closest("[data-sort]");
  if (sel) { localSet("sort:" + sel.dataset.sort, sel.value); route(); }
});
view.addEventListener("click", async (e) => {
  const rm = e.target.closest("[data-remove]");
  if (rm && confirm("Remove this folder from RCM? Your files stay where they are.")) {
    await api(`/api/libraries/${rm.dataset.remove}`, { method: "DELETE" });
    toast("Folder removed"); route();
  }
  if (e.target.id === "rescan") {
    await api("/api/rescan", { method: "POST" });
    toast("Rescan started"); setTimeout(route, 1500);
  }
  if (e.target.id === "play-all" || e.target.closest("[data-track]")) {
    const ids = JSON.parse(view.dataset.queue || "[]");
    const start = e.target.id === "play-all" ? 0 : Number(e.target.closest("[data-track]").dataset.track);
    playQueue(ids.map((id) => shown.get(id)), start);
  }
});
view.addEventListener("submit", async (e) => {
  if (e.target.id !== "add-lib") return;
  e.preventDefault();
  const path = new FormData(e.target).get("path");
  try {
    await api("/api/libraries", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ path }) });
    toast("Folder added — scanning"); setTimeout(route, 800);
  } catch (err) { toast(err.message); }
});

let searchTimer;
$("#search").addEventListener("input", (e) => {
  clearTimeout(searchTimer);
  const q = e.target.value.trim();
  searchTimer = setTimeout(() => { location.hash = q ? `#/search/${encodeURIComponent(q)}` : "#/home"; }, 300);
});

// ---------- player dialog: video, audiobooks, photos, PDFs, comics, EPUBs ----------
const dlg = $("#player");
let current = null;

function putProgress(id, pos) {
  fetch(`/api/items/${id}/progress`, {
    method: "PUT", keepalive: true, headers: { "Content-Type": "application/json" }, body: JSON.stringify({ position: pos }),
  }).catch(() => {});
}
// Media saves every ~10 s of playback; readers save on every page or chapter change.
function saveProgress(force) {
  if (!current || !current.getPos) return;
  const pos = current.getPos();
  if (pos == null || isNaN(pos)) return;
  if (!force && Math.abs(pos - current.lastSaved) < current.minStep) return;
  if (pos === current.lastSaved) return;
  current.lastSaved = pos;
  current.item.position = pos; // so reopening without a page reload resumes correctly
  putProgress(current.item.id, pos);
}
function note(text) {
  const el = $("#pl-note");
  el.textContent = el.textContent ? `${el.textContent} · ${text}` : text;
}

// teardown stops playback and saves progress, but leaves the dialog open
function teardown() {
  if (!current) return;
  saveProgress(true);
  const m = current.media;
  if (m) { m.pause(); m.removeAttribute("src"); m.load(); } // stop the network stream
  if (current.cleanup) current.cleanup();
  $("#pl-body").innerHTML = "";
  $("#pl-extra").innerHTML = "";
  current = null;
}

function showPlayer(it, { reader = false } = {}) {
  teardown();
  $("#pl-title").textContent = it.kind === "episode" ? `${it.show} — ${it.title}` : it.title;
  $("#pl-sub").textContent = itemSub(it);
  $("#pl-download").href = streamURL(it) + "?download=1";
  $("#pl-note").textContent = "";
  $("#pl-restart").hidden = true;
  $("#pl-speed").hidden = true;
  dlg.classList.toggle("reader", reader);
  current = { item: it, media: null, lastSaved: it.position || 0, minStep: 10, getPos: null, onKey: null, cleanup: null };
  if (!dlg.open) dlg.showModal();
  return $("#pl-body");
}

function closePlayer() {
  teardown();
  if (dlg.open) dlg.close();
  dlg.classList.remove("reader");
  if (/^#\/(home|tv|books|comics|audiobooks)/.test(location.hash) || !location.hash) route(); // refresh progress bars
}
$("#pl-close").addEventListener("click", closePlayer);
dlg.addEventListener("cancel", (e) => { e.preventDefault(); closePlayer(); });
$("#pl-restart").addEventListener("click", () => {
  if (current && current.media) { current.media.currentTime = 0; current.media.play(); $("#pl-restart").hidden = true; $("#pl-note").textContent = ""; }
});
$("#pl-speed").addEventListener("change", (e) => {
  const m = current && current.media;
  if (!m) return;
  m.playbackRate = m.defaultPlaybackRate = Number(e.target.value);
  localSet("speed:" + m.tagName.toLowerCase(), e.target.value);
});
document.addEventListener("keydown", (e) => {
  if (dlg.open && current && current.onKey && !(e.target.closest && e.target.closest("input, select, textarea"))) current.onKey(e);
});
window.addEventListener("beforeunload", () => saveProgress(true));

function openItem(it, card) {
  if (it.kind === "music") return playQueue([it], 0);
  if (it.kind === "photo") {
    const group = card && card.closest(".grid, .row");
    const list = group ? [...group.querySelectorAll("[data-id]")].map((el) => shown.get(Number(el.dataset.id))).filter((x) => x && x.kind === "photo") : [it];
    return openPhotos(list.length ? list : [it], Math.max(0, list.indexOf(it)));
  }
  if (it.kind === "comic") return openComic(it);
  if (it.kind === "book" && it.ext === "epub") return openBook(it);
  if (it.kind === "book" && it.ext === "pdf") {
    showPlayer(it).innerHTML = `<iframe src="${streamURL(it)}" title="${esc(it.title)}"></iframe>`;
    return;
  }
  if (["movie", "episode", "audiobook"].includes(it.kind)) return openMedia(it);
  return openDownload(it);
}

function openDownload(it, reason) {
  const body = current && current.item === it ? $("#pl-body") : showPlayer(it);
  dlg.classList.remove("reader");
  body.innerHTML = `<div class="doc-box"><p style="font-size:2.5rem;margin:0">${KIND_ICON[it.kind] || "📄"}</p>
    <p><b>${esc(it.title)}</b> · ${it.ext.toUpperCase()} · ${fmtSize(it.size)}</p>
    <p>${esc(reason || `${it.ext.toUpperCase()} files can't be opened in the browser yet. Download it to read in your favorite app.`)}</p>
    <p><a class="btn primary" href="${streamURL(it)}?download=1">Download</a></p></div>`;
}

// ---- video and audiobooks ----
function openMedia(it) {
  const body = showPlayer(it);
  const tag = it.kind === "audiobook" ? "audio" : "video";
  const el = `<${tag} controls autoplay playsinline preload="metadata" src="${streamURL(it)}"></${tag}>`;
  body.innerHTML = tag === "audio" ? `<div class="audio-wrap">${el}</div>` : el;
  const m = body.querySelector(tag);
  current.media = m;
  current.getPos = () => m.currentTime;
  const speed = $("#pl-speed");
  speed.hidden = false;
  speed.value = localGet("speed:" + tag) || "1";
  m.addEventListener("loadedmetadata", () => {
    m.playbackRate = m.defaultPlaybackRate = Number(speed.value);
    const p = it.position || 0;
    if (p > 30 && (!m.duration || p < m.duration * 0.95)) {
      m.currentTime = p;
      note(`Resumed at ${fmtTime(p)}`);
      $("#pl-restart").hidden = false;
    }
  });
  m.addEventListener("timeupdate", () => saveProgress(false));
  m.addEventListener("pause", () => saveProgress(true));
  m.addEventListener("error", () => {
    note(`This browser can't play this ${it.ext.toUpperCase()} file's format. Try Chrome, or use Download to play it in VLC. (Transcoding comes in a later phase.)`);
  });
  if (tag === "video") loadSubtitles(it, m);
  else loadChapters(it, m);
}

async function loadSubtitles(it, video) {
  const subs = await api(`/api/items/${it.id}/subtitles`).catch(() => []);
  if (!subs.length || !current || current.media !== video) return;
  for (const s of subs) {
    const t = document.createElement("track");
    t.kind = "subtitles";
    t.label = s.label;
    if (s.lang) t.srclang = s.lang;
    t.src = `/api/items/${it.id}/subtitles/${s.id}`;
    video.appendChild(t);
  }
  note(`${subs.length} subtitle track${subs.length > 1 ? "s" : ""} in the captions menu`);
}

async function loadChapters(it, m) {
  const ch = await api(`/api/items/${it.id}/chapters`).catch(() => []);
  if (ch.length < 2 || !current || current.media !== m) return;
  const box = $("#pl-extra");
  box.innerHTML = `<ol class="chapters">${ch.map((c, i) => `<li data-ch="${i}"><span class="grow">${esc(c.title)}</span><span class="d">${fmtTime(c.start)}</span></li>`).join("")}</ol>`;
  box.onclick = (e) => {
    const li = e.target.closest("[data-ch]");
    if (li) { m.currentTime = ch[Number(li.dataset.ch)].start + 0.01; m.play(); }
  };
  let last = -1;
  const mark = () => {
    let cur = 0;
    ch.forEach((c, i) => { if (m.currentTime >= c.start) cur = i; });
    if (cur === last) return;
    last = cur;
    box.querySelectorAll("li").forEach((li, i) => li.classList.toggle("playing", i === cur));
    box.querySelector("li.playing")?.scrollIntoView({ block: "nearest" });
  };
  m.addEventListener("timeupdate", mark);
  mark();
}

// ---- photos ----
function openPhotos(list, i) {
  const it = list[i];
  const body = showPlayer(it);
  $("#pl-sub").textContent = `${it.album ? it.album + " · " : ""}${i + 1} of ${list.length}`;
  // HEIC and other formats some browsers can't show fall back to the JPEG thumbnail.
  body.innerHTML = `<div class="lightbox"><img src="${streamURL(it)}" alt="${esc(it.title)}" onerror="this.onerror=null;this.src='${thumbURL(it.id)}'">
    ${i > 0 ? `<button class="nav prev" aria-label="Previous photo">‹</button>` : ""}
    ${i < list.length - 1 ? `<button class="nav next" aria-label="Next photo">›</button>` : ""}</div>`;
  const go = (d) => { const j = i + d; if (j >= 0 && j < list.length) openPhotos(list, j); };
  body.querySelector(".prev")?.addEventListener("click", () => go(-1));
  body.querySelector(".next")?.addEventListener("click", () => go(1));
  current.onKey = (e) => { if (e.key === "ArrowLeft") go(-1); else if (e.key === "ArrowRight") go(1); };
  if (list[i + 1]) new Image().src = streamURL(list[i + 1]);
}

// ---- comics ----
async function openComic(it) {
  const body = showPlayer(it, { reader: true });
  body.innerHTML = `<div class="doc-box">Opening…</div>`;
  let pages;
  try {
    pages = (await api(`/api/items/${it.id}/pages`)).pages;
  } catch (err) {
    if (current && current.item === it) openDownload(it, err.message);
    return;
  }
  if (!current || current.item !== it) return;
  it.duration = pages;
  // position = pages read (1-based); start over if it was finished
  let page = it.position > 0 && it.position < pages * 0.99 ? Math.max(0, Math.round(it.position) - 1) : 0;
  let fit = localGet("comic:fit") || "height";
  body.innerHTML = `<div class="comic-stage"><img alt=""></div>
    <div class="reader-bar">
      <button class="btn small" data-act="prev" aria-label="Previous page">‹</button>
      <input type="range" min="1" max="${pages}" value="${page + 1}" aria-label="Page">
      <span class="pg"></span>
      <button class="btn small" data-act="next" aria-label="Next page">›</button>
      <button class="btn small" data-act="fit"></button>
    </div>`;
  const stage = $(".comic-stage", body), img = $("img", stage), range = $("input", body), pg = $(".pg", body), fitBtn = $('[data-act="fit"]', body);
  const pageURL = (n) => `/api/items/${it.id}/pages/${n}`;
  current.getPos = () => page + 1;
  current.minStep = 0.5;
  const show = (n) => {
    if (n < 0 || n >= pages) return;
    page = n;
    img.src = pageURL(n);
    img.alt = `Page ${n + 1}`;
    range.value = n + 1;
    pg.textContent = `${n + 1} / ${pages}`;
    stage.scrollTop = 0;
    if (n + 1 < pages) new Image().src = pageURL(n + 1); // preload
    saveProgress(false);
  };
  const setFit = (f) => {
    fit = f;
    stage.className = `comic-stage fit-${f}`;
    fitBtn.textContent = f === "height" ? "Fit width" : "Fit page";
    localSet("comic:fit", f);
  };
  setFit(fit);
  $(".reader-bar", body).addEventListener("click", (e) => {
    const act = e.target.closest("[data-act]")?.dataset.act;
    if (act === "prev") show(page - 1);
    if (act === "next") show(page + 1);
    if (act === "fit") setFit(fit === "height" ? "width" : "height");
  });
  range.addEventListener("input", () => show(Number(range.value) - 1));
  // tap the left third to go back, anywhere else to go forward
  stage.addEventListener("click", (e) => {
    const r = stage.getBoundingClientRect();
    show(e.clientX - r.left < r.width / 3 ? page - 1 : page + 1);
  });
  current.onKey = (e) => {
    if (e.key === "ArrowRight" || e.key === "PageDown") { e.preventDefault(); show(page + 1); }
    if (e.key === "ArrowLeft" || e.key === "PageUp") { e.preventDefault(); show(page - 1); }
  };
  show(page);
}

// ---- EPUB ----
const BOOK_THEMES = {
  light: { bg: "#ffffff", fg: "#1c1917", link: "#c2410c" },
  sepia: { bg: "#f4ecd8", fg: "#3b2f22", link: "#9a3412" },
  dark: { bg: "#161616", fg: "#d6d3d1", link: "#fb923c" },
};

async function openBook(it) {
  const body = showPlayer(it, { reader: true });
  body.innerHTML = `<div class="doc-box">Opening…</div>`;
  let book;
  try {
    book = await api(`/api/items/${it.id}/book`);
  } catch (err) {
    if (current && current.item === it) openDownload(it, err.message);
    return;
  }
  if (!current || current.item !== it) return;
  const n = book.spine.length;
  it.duration = n;
  if (book.author) $("#pl-sub").textContent = book.author;
  // position = chapter index + how far through it (0..1)
  let idx = 0, pendingFrac = 0;
  if (it.position > 0 && it.position < n * 0.99) {
    idx = Math.min(n - 1, Math.floor(it.position));
    pendingFrac = it.position - idx;
  }
  const prefs = { size: Number(localGet("book:size")) || 110, theme: localGet("book:theme") || "sepia" };
  if (!BOOK_THEMES[prefs.theme]) prefs.theme = "sepia";
  // sandbox without allow-scripts: book content can't run code, but we can still style it
  body.innerHTML = `<iframe class="book-frame" sandbox="allow-same-origin" title="${esc(it.title)}"></iframe>
    <div class="reader-bar">
      <button class="btn small" data-act="prev" aria-label="Previous chapter">‹</button>
      <select class="toc" aria-label="Contents"></select>
      <button class="btn small" data-act="next" aria-label="Next chapter">›</button>
      <span class="spacer"></span>
      <button class="btn small" data-act="smaller" aria-label="Smaller text">A−</button>
      <button class="btn small" data-act="bigger" aria-label="Bigger text">A+</button>
      <select class="theme" aria-label="Page color">
        <option value="light">Light</option><option value="sepia">Sepia</option><option value="dark">Dark</option>
      </select>
    </div>`;
  const frame = $("iframe", body), toc = $(".toc", body), theme = $(".theme", body);
  const entries = book.toc.length ? book.toc : book.spine.map((_, i) => ({ title: `Section ${i + 1} of ${n}`, spine: i, depth: 0 }));
  toc.innerHTML = entries.map((t, k) => `<option value="${k}">${"   ".repeat(Math.min(t.depth, 4))}${esc(t.title)}</option>`).join("");
  theme.value = prefs.theme;

  const syncToc = () => {
    let best = 0;
    entries.forEach((t, k) => { if (t.spine <= idx) best = k; });
    toc.value = String(best);
  };
  const applyStyle = () => {
    const d = frame.contentDocument;
    if (!d || !d.documentElement) return;
    const t = BOOK_THEMES[prefs.theme];
    let s = d.getElementById("rcm-reader-style");
    if (!s) {
      s = d.createElementNS("http://www.w3.org/1999/xhtml", "style");
      s.id = "rcm-reader-style";
      (d.head || d.documentElement).appendChild(s);
    }
    s.textContent = `html{background:${t.bg}!important}
      body{max-width:40em;margin:0 auto!important;padding:2em 1.4em 5em!important;font-size:${prefs.size}%!important;
        line-height:1.65!important;background:${t.bg}!important;color:${t.fg}!important}
      body *{color:inherit!important;background-color:transparent!important}
      a{color:${t.link}!important} img,svg,video{max-width:100%!important;height:auto!important}`;
    frame.style.background = t.bg;
  };
  const fraction = () => {
    const w = frame.contentWindow, d = frame.contentDocument;
    if (!w || !d || !d.documentElement) return 0;
    const max = d.documentElement.scrollHeight - w.innerHeight;
    return max > 0 ? Math.min(0.999, Math.max(0, w.scrollY / max)) : 0;
  };
  const load = (i, frac = 0) => {
    if (i < 0 || i >= n) return;
    idx = i;
    pendingFrac = frac;
    frame.src = book.spine[i];
    syncToc();
  };
  current.getPos = () => idx + fraction();
  current.minStep = 0.001;
  let scrollTimer = 0;
  current.cleanup = () => clearTimeout(scrollTimer);

  frame.addEventListener("load", () => {
    const w = frame.contentWindow, d = frame.contentDocument;
    if (!w || !d) return;
    // a link inside the book may have moved us to another chapter
    const here = decodeURIComponent(w.location.pathname);
    const j = book.spine.findIndex((u) => decodeURIComponent(u) === here);
    if (j >= 0) idx = j;
    syncToc();
    applyStyle();
    if (pendingFrac > 0) {
      const f = pendingFrac;
      pendingFrac = 0;
      requestAnimationFrame(() => w.scrollTo(0, f * (d.documentElement.scrollHeight - w.innerHeight)));
    }
    w.addEventListener("scroll", () => {
      clearTimeout(scrollTimer);
      scrollTimer = setTimeout(() => saveProgress(false), 1000);
    });
    // keys pressed while reading go to the iframe, so forward them
    d.addEventListener("keydown", (e) => {
      if (e.key === "Escape") closePlayer();
      else if (current && current.onKey) current.onKey(e);
    });
    saveProgress(false);
  });

  $(".reader-bar", body).addEventListener("click", (e) => {
    const act = e.target.closest("[data-act]")?.dataset.act;
    if (act === "prev") load(idx - 1);
    if (act === "next") load(idx + 1);
    if (act === "smaller" || act === "bigger") {
      prefs.size = Math.min(200, Math.max(70, prefs.size + (act === "bigger" ? 10 : -10)));
      localSet("book:size", prefs.size);
      applyStyle();
    }
  });
  toc.addEventListener("change", () => load(entries[Number(toc.value)].spine));
  theme.addEventListener("change", () => { prefs.theme = theme.value; localSet("book:theme", prefs.theme); applyStyle(); });
  current.onKey = (e) => {
    if (e.key === "ArrowRight") { e.preventDefault(); load(idx + 1); }
    if (e.key === "ArrowLeft") { e.preventDefault(); load(idx - 1); }
  };
  load(idx, pendingFrac);
}

// ---------- music bar ----------
const audio = $("#audio");
const queue = { items: [], i: 0 };

function playQueue(items, i) {
  queue.items = items.filter(Boolean);
  playIndex(i);
}
function playIndex(i) {
  if (i < 0 || i >= queue.items.length) return;
  queue.i = i;
  const t = queue.items[i];
  audio.src = streamURL(t);
  audio.play().catch(() => {});
  $("#ab-title").textContent = t.title;
  $("#ab-sub").textContent = [t.artist, t.album].filter(Boolean).join(" — ");
  $("#audio-bar").hidden = false;
  document.title = `♪ ${t.title} — Rough Country Media`;
  markPlaying();
}
function markPlaying() {
  const id = !$("#audio-bar").hidden && queue.items[queue.i] ? queue.items[queue.i].id : null;
  document.querySelectorAll("[data-tid]").forEach((li) => li.classList.toggle("playing", Number(li.dataset.tid) === id));
}
audio.addEventListener("ended", () => playIndex(queue.i + 1));
$("#ab-next").addEventListener("click", () => playIndex(queue.i + 1));
$("#ab-prev").addEventListener("click", () => (audio.currentTime > 3 ? (audio.currentTime = 0) : playIndex(queue.i - 1)));
$("#ab-close").addEventListener("click", () => {
  audio.pause(); audio.removeAttribute("src"); audio.load();
  $("#audio-bar").hidden = true; document.title = "Rough Country Media"; markPlaying();
});

// ---------- add menu + drag and drop import ----------
const menu = $("#add-menu");
$("#add-btn").addEventListener("click", (e) => { e.stopPropagation(); menu.hidden = !menu.hidden; });
document.addEventListener("click", (e) => { if (!menu.contains(e.target)) menu.hidden = true; });
menu.addEventListener("click", (e) => {
  const b = e.target.closest("[data-pick]");
  if (!b) return;
  menu.hidden = true;
  $(b.dataset.pick === "folder" ? "#pick-folder" : "#pick-files").click();
});
for (const id of ["#pick-files", "#pick-folder"]) {
  $(id).addEventListener("change", (e) => {
    const files = [...e.target.files].map((f) => ({ file: f, path: f.webkitRelativePath || f.name }));
    e.target.value = "";
    upload(files);
  });
}

const drop = $("#drop");
let dragDepth = 0;
const hasFiles = (e) => e.dataTransfer && [...e.dataTransfer.types].includes("Files");
window.addEventListener("dragenter", (e) => { if (!hasFiles(e)) return; e.preventDefault(); dragDepth++; drop.hidden = false; });
window.addEventListener("dragover", (e) => { if (hasFiles(e)) e.preventDefault(); });
window.addEventListener("dragleave", (e) => { if (!hasFiles(e)) return; if (--dragDepth <= 0) { dragDepth = 0; drop.hidden = true; } });
window.addEventListener("drop", async (e) => {
  if (!hasFiles(e)) return;
  e.preventDefault();
  dragDepth = 0; drop.hidden = true;
  upload(await filesFromDrop(e.dataTransfer));
});

// Walk dropped folders so whole show/album folders keep their structure.
async function filesFromDrop(dt) {
  const entries = [...dt.items].map((i) => i.webkitGetAsEntry && i.webkitGetAsEntry()).filter(Boolean); // must run before any await
  if (!entries.length) return [...dt.files].map((f) => ({ file: f, path: f.name }));
  const out = [];
  async function walk(entry, prefix) {
    if (entry.isFile) {
      const f = await new Promise((res, rej) => entry.file(res, rej));
      out.push({ file: f, path: prefix + f.name });
    } else if (entry.isDirectory) {
      const reader = entry.createReader();
      for (;;) {
        const batch = await new Promise((res, rej) => reader.readEntries(res, rej));
        if (!batch.length) break;
        for (const child of batch) await walk(child, prefix + entry.name + "/");
      }
    }
  }
  for (const e of entries) await walk(e, "");
  return out;
}

let uploading = false;
const pendingUploads = [];
function upload(files) {
  files = files.filter((f) => !f.path.split("/").some((p) => p.startsWith(".")));
  if (!files.length) return;
  const list = $("#up-list");
  $("#uploads").hidden = false;
  for (const f of files) {
    const li = document.createElement("li");
    li.innerHTML = `<div class="name"><span>${esc(f.path)}</span><span class="st muted">${fmtSize(f.file.size)}</span></div><progress max="100" value="0"></progress>`;
    list.appendChild(li);
    pendingUploads.push({ ...f, li });
  }
  if (!uploading) runUploads();
}
async function runUploads() {
  uploading = true;
  let done = 0, failed = 0;
  $("#up-title").textContent = "Uploading…";
  while (pendingUploads.length) {
    const job = pendingUploads.shift();
    const st = $(".st", job.li), bar = $("progress", job.li);
    try {
      const res = await putFile(job, (p) => (bar.value = p));
      bar.remove();
      if (res.skipped) { st.textContent = "skipped (not media)"; st.className = "st skip"; }
      else { st.textContent = "✓ " + res.folder; st.className = "st ok"; done++; }
    } catch (err) {
      bar.remove(); st.textContent = "✕ " + err.message; st.className = "st err"; failed++;
    }
  }
  uploading = false;
  $("#up-title").textContent = `Done: ${done} added${failed ? `, ${failed} failed` : ""}`;
  if (done) { toast(`${done} file${done > 1 ? "s" : ""} added`); route(); }
}
function putFile(job, onProgress) {
  return new Promise((resolve, reject) => {
    const xhr = new XMLHttpRequest();
    xhr.open("PUT", `/api/import?path=${encodeURIComponent(job.path)}`);
    xhr.upload.onprogress = (e) => e.lengthComputable && onProgress((e.loaded / e.total) * 100);
    xhr.onload = () => {
      let data = {};
      try { data = JSON.parse(xhr.responseText); } catch {}
      xhr.status < 300 ? resolve(data) : reject(new Error(data.error || xhr.statusText));
    };
    xhr.onerror = () => reject(new Error("network error"));
    xhr.send(job.file);
  });
}
$("#up-close").addEventListener("click", () => {
  $("#uploads").hidden = true;
  if (!uploading) $("#up-list").innerHTML = "";
});
window.addEventListener("beforeunload", (e) => { if (uploading) { e.preventDefault(); e.returnValue = ""; } });

route();
