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
const streamURL = (it) => `/api/items/${it.id}/stream`;
const KIND_ICON = { movie: "🎬", episode: "📺", music: "🎵", audiobook: "🎧", book: "📖", comic: "💬", photo: "🖼" };
const pct = (it) => (it.duration && it.position ? Math.min(100, (it.position / it.duration) * 100) : 0);

// ---------- cards ----------
function itemSub(it) {
  switch (it.kind) {
    case "movie": return it.year || "";
    case "episode": return `${it.show} · S${String(it.season).padStart(2, "0")}E${String(it.episode).padStart(2, "0")}`;
    case "music": case "audiobook": return it.artist || it.album || "";
    case "photo": return it.album || "";
    default: return it.year || it.ext.toUpperCase();
  }
}
function itemCard(it, shape = "") {
  const label = it.kind === "episode" ? it.show : it.title;
  const img = it.kind === "photo" ? `<img loading="lazy" src="${streamURL(it)}" alt="">` : "";
  return `<button class="card" data-id="${it.id}">
    <div class="art ${shape}" style="${artStyle(label)}">${img}
      <span class="kind-icon">${KIND_ICON[it.kind] || ""}</span>${img ? "" : esc(label)}
      ${pct(it) ? `<span class="bar" style="width:${pct(it)}%"></span>` : ""}
    </div>
    <div class="t">${esc(it.kind === "episode" ? it.title : it.title)}</div>
    <div class="s">${esc(itemSub(it))}</div>
  </button>`;
}
function linkCard(href, title, sub, shape = "") {
  return `<a class="card" href="${href}">
    <div class="art ${shape}" style="${artStyle(title)}">${esc(title)}</div>
    <div class="t">${esc(title)}</div><div class="s">${esc(sub)}</div></a>`;
}
// clicking a card with data-id opens that item; cache items by id per render
let shown = new Map();
function remember(items) { for (const it of items) shown.set(it.id, it); return items; }
view.addEventListener("click", (e) => {
  const card = e.target.closest("[data-id]");
  if (!card) return;
  const it = shown.get(Number(card.dataset.id));
  if (it) openItem(it);
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
    return `<div class="page-head"><h1>Home</h1></div>
      <div class="stats">${chip("movie", "Movies", "#/movies")}${chip("episode", "Episodes", "#/tv")}
        ${chip("music", "Songs", "#/music")}${chip("audiobook", "Audiobooks", "#/audiobooks")}
        ${chip("book", "Books", "#/books")}${chip("comic", "Comics", "#/comics")}${chip("photo", "Photos", "#/photos")}</div>
      ${cont.length ? `<h2>Continue watching</h2><div class="row">${cont.map((i) => itemCard(i)).join("")}</div>` : ""}
      <h2>Recently added</h2><div class="row">${recent.map((i) => itemCard(i, i.kind === "music" ? "square" : "")).join("")}</div>`;
  },

  async movies() { return gridPage("Movies", "movie", "Drop movie files here, like “The Matrix (1999).mkv”."); },
  async books() { return gridPage("Books", "book", "EPUB, PDF, MOBI and AZW3 files are supported."); },
  async comics() { return gridPage("Comics", "comic", "CBZ, CBR and CB7 files are supported."); },
  async audiobooks() { return gridPage("Audiobooks", "audiobook", "M4B audiobook files show up here.", "square"); },

  async photos() {
    const items = remember(await api("/api/items?kind=photo&sort=added"));
    if (!items.length) return `<h1>Photos</h1>` + emptyState("No photos yet", "JPG, PNG, GIF, WebP and HEIC files are supported.");
    return `<div class="page-head"><h1>Photos</h1><span class="muted">${items.length} photos</span></div>
      <div class="grid">${items.map((i) => itemCard(i, "square")).join("")}</div>`;
  },

  async tv(show) {
    if (show) return showPage(show);
    const shows = await api("/api/shows");
    if (!shows.length) return `<h1>TV</h1>` + emptyState("No TV shows yet", "Episodes are detected from names like “Show.Name.S01E02.mkv” or “Show - 1x02.mp4”.");
    return `<div class="page-head"><h1>TV</h1><span class="muted">${shows.length} shows</span></div>
      <div class="grid">${shows.map((s) => linkCard(`#/tv/${encodeURIComponent(s.show)}`, s.show,
        `${s.seasons} season${s.seasons > 1 ? "s" : ""} · ${s.episodes} ep`)).join("")}</div>`;
  },

  async music(artist, album) {
    if (album !== undefined) return albumPage(artist, album);
    const albums = await api("/api/albums");
    if (!albums.length) return `<h1>Music</h1>` + emptyState("No music yet", "MP3, FLAC, M4A, OGG, Opus and WAV files are supported. Tags are read automatically.");
    return `<div class="page-head"><h1>Music</h1><span class="muted">${albums.length} albums</span></div>
      <div class="grid">${albums.map((a) => linkCard(`#/music/${encodeURIComponent(a.album_artist)}/${encodeURIComponent(a.album)}`,
        a.album, `${a.album_artist}${a.year ? " · " + a.year : ""}`, "square")).join("")}</div>`;
  },

  async search(q) {
    const items = remember(await api(`/api/items?q=${encodeURIComponent(q)}&limit=300`));
    if (!items.length) return `<h1>Search</h1><p class="muted">Nothing matches “${esc(q)}”.</p>`;
    const groups = {};
    for (const it of items) (groups[it.kind] ||= []).push(it);
    const names = { movie: "Movies", episode: "TV episodes", music: "Songs", audiobook: "Audiobooks", book: "Books", comic: "Comics", photo: "Photos" };
    return `<h1>Results for “${esc(q)}”</h1>` + Object.entries(groups).map(([k, list]) =>
      `<h2>${names[k] || k}</h2><div class="grid">${list.map((i) => itemCard(i, k === "music" || k === "photo" ? "square" : "")).join("")}</div>`).join("");
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
          · ${st.total_items} items · watcher ${st.watching ? "on" : "off"} · ffprobe ${st.ffprobe ? "found" : "<b>missing</b> (install ffmpeg for durations)"}</p>
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
  return `<a class="crumb" href="#/music">← Music</a>
    <div class="page-head"><div><h1>${esc(album)}</h1><span class="muted">${esc(artist)} · ${tracks.length} tracks${total ? " · " + fmtTime(total) : ""}</span></div>
      <button class="btn primary" id="play-all">▶ Play album</button></div>
    <ul class="list" id="tracks">${tracks.map((t, i) => `<li data-track="${i}" data-tid="${t.id}"><span class="n">${t.track || i + 1}</span>
      <span class="grow">${esc(t.title)}${t.artist && t.artist !== artist ? ` <span class="muted">— ${esc(t.artist)}</span>` : ""}</span>
      <span class="d">${t.duration ? fmtTime(t.duration) : ""}</span></li>`).join("")}</ul>`;
}

// ---------- router ----------
function localGet(k) { try { return localStorage.getItem(k); } catch { return null; } }
function localSet(k, v) { try { localStorage.setItem(k, v); } catch {} }

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

// ---------- player dialog (video, audiobooks, photos, PDFs) ----------
const dlg = $("#player");
let current = null, saveTimer = null;

function saveProgress(force) {
  if (!current || !current.media) return;
  const pos = current.media.currentTime;
  if (!force && Math.abs(pos - current.lastSaved) < 10) return;
  current.lastSaved = pos;
  fetch(`/api/items/${current.item.id}/progress`, {
    method: "PUT", keepalive: true, headers: { "Content-Type": "application/json" }, body: JSON.stringify({ position: pos }),
  }).catch(() => {});
}

function openItem(it) {
  if (it.kind === "music") return playQueue([it], 0);
  const body = $("#pl-body");
  $("#pl-title").textContent = it.kind === "episode" ? `${it.show} — ${it.title}` : it.title;
  $("#pl-sub").textContent = itemSub(it);
  $("#pl-download").href = streamURL(it) + "?download=1";
  $("#pl-note").textContent = "";
  $("#pl-restart").hidden = true;
  current = { item: it, media: null, lastSaved: 0 };

  if (it.kind === "movie" || it.kind === "episode" || it.kind === "audiobook") {
    const tag = it.kind === "audiobook" ? "audio" : "video";
    body.innerHTML = `<${tag} controls autoplay playsinline preload="metadata" src="${streamURL(it)}"></${tag}>`;
    const m = body.firstElementChild;
    if (tag === "audio") m.style.cssText = "width:100%;padding:30px 20px;box-sizing:border-box";
    current.media = m;
    m.addEventListener("loadedmetadata", () => {
      const p = it.position || 0;
      if (p > 30 && (!m.duration || p < m.duration * 0.95)) {
        m.currentTime = p;
        $("#pl-note").textContent = `Resumed at ${fmtTime(p)}`;
        $("#pl-restart").hidden = false;
      }
    });
    m.addEventListener("timeupdate", () => saveProgress(false));
    m.addEventListener("pause", () => saveProgress(true));
    m.addEventListener("error", () => {
      $("#pl-note").textContent = `This browser can't play this ${it.ext.toUpperCase()} file's format. Try Chrome, or use Download to play it in VLC. (Transcoding comes in a later phase.)`;
    });
  } else if (it.kind === "photo") {
    body.innerHTML = `<img src="${streamURL(it)}" alt="${esc(it.title)}">`;
  } else if (it.ext === "pdf") {
    body.innerHTML = `<iframe src="${streamURL(it)}" title="${esc(it.title)}"></iframe>`;
  } else {
    body.innerHTML = `<div class="doc-box"><p style="font-size:2.5rem;margin:0">${KIND_ICON[it.kind] || "📄"}</p>
      <p><b>${esc(it.title)}</b> · ${it.ext.toUpperCase()} · ${fmtSize(it.size)}</p>
      <p>A built-in ${it.kind === "comic" ? "comic" : "e-book"} reader is coming in phase 2. For now, download it to read in your favorite app.</p>
      <p><a class="btn primary" href="${streamURL(it)}?download=1">Download</a></p></div>`;
  }
  if (!dlg.open) dlg.showModal();
}

function closePlayer() {
  saveProgress(true);
  const m = current && current.media;
  if (m) { m.pause(); m.removeAttribute("src"); m.load(); } // stop the network stream
  $("#pl-body").innerHTML = "";
  current = null;
  if (dlg.open) dlg.close();
  if (/^#\/(home|tv)/.test(location.hash) || !location.hash) route(); // refresh progress bars
}
$("#pl-close").addEventListener("click", closePlayer);
dlg.addEventListener("cancel", (e) => { e.preventDefault(); closePlayer(); });
$("#pl-restart").addEventListener("click", () => {
  if (current && current.media) { current.media.currentTime = 0; current.media.play(); $("#pl-restart").hidden = true; $("#pl-note").textContent = ""; }
});
window.addEventListener("beforeunload", () => saveProgress(true));

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
