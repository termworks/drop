import { Screen, paint } from "/term.js";

const peersList = document.querySelector("#peers");
const noPeers = document.querySelector("#no-peers");
const logList = document.querySelector("#log");
const pick = document.querySelector("#pick");
const compose = document.querySelector("#compose");
const bodyInput = document.querySelector("#body");
const withHeader = document.querySelector("#with");

let current = null;

async function api(path, options) {
  const res = await fetch(path, options);
  const body = await res.json().catch(() => ({}));
  if (!res.ok) throw new Error(body.error || res.statusText);
  return body;
}

function when(at) {
  return new Date(at).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
}

// Links are the one thing rendered as markup, and only when the whole body is a URL we parsed
// ourselves. Everything else is set as text, so a message can never introduce elements.
function fillBubble(el, m) {
  if (m.kind === "link") {
    let url;
    try { url = new URL(m.body); } catch { url = null; }
    if (url && (url.protocol === "http:" || url.protocol === "https:")) {
      const tag = document.createElement("span");
      tag.className = "tag";
      tag.textContent = "link";
      const a = document.createElement("a");
      a.href = url.href;
      a.target = "_blank";
      a.rel = "noopener noreferrer";
      a.textContent = url.href;
      el.append(tag, a);
      return;
    }
  }
  if (m.kind === "file") {
    const tag = document.createElement("span");
    tag.className = "tag";
    tag.textContent = m.extra ? `file · ${m.extra}` : "file";
    el.append(tag, document.createTextNode(m.body));
    return;
  }
  el.textContent = m.body;
}

function render(m) {
  const li = document.createElement("li");
  li.className = m.mine ? "mine" : "";
  if (m.kind === "event") li.className = "event";

  const bubble = document.createElement("div");
  bubble.className = "bubble";
  fillBubble(bubble, m);
  li.append(bubble);

  if (m.kind !== "event") {
    const meta = document.createElement("div");
    meta.className = "meta";
    meta.textContent = when(m.at);
    li.append(meta);
  }
  return li;
}

function atBottom() {
  return logList.scrollHeight - logList.scrollTop - logList.clientHeight < 60;
}

function append(m) {
  const row = render(m);
  const stick = atBottom();
  logList.append(row);
  if (stick) logList.scrollTop = logList.scrollHeight;
  return row;
}

async function loadPeers() {
  const peers = await api("/api/peers");
  peersList.replaceChildren();
  noPeers.hidden = peers.length > 0;

  for (const p of peers) {
    const li = document.createElement("li");
    const button = document.createElement("button");
    button.textContent = p.name;
    button.setAttribute("aria-current", String(p.name === current));

    const state = document.createElement("span");
    state.className = p.unread ? "state waiting" : "state";
    state.textContent = p.unread ? `${p.unread} waiting` : (p.paired ? "" : "not paired");
    button.append(state);

    button.addEventListener("click", () => open(p));
    li.append(button);
    peersList.append(li);
  }
}

async function open(peer) {
  current = peer.name;
  withHeader.hidden = false;
  withHeader.querySelector(".name").textContent = peer.name;
  withHeader.querySelector(".id").textContent = peer.id.slice(-12);
  pick.hidden = true;
  compose.hidden = false;

  stopWatching();
  termPanel.hidden = true;
  const history = await api(`/api/log/${encodeURIComponent(peer.name)}`);
  logList.replaceChildren(...history.map(render));
  logList.scrollTop = logList.scrollHeight;

  for (const b of peersList.querySelectorAll("button")) {
    b.setAttribute("aria-current", String(b.textContent.startsWith(peer.name)));
  }
  bodyInput.focus();
}

compose.addEventListener("submit", async (e) => {
  e.preventDefault();
  const body = bodyInput.value.trim();
  if (!body || !current) return;

  // A bare URL is sent as a link, which is what makes it openable on the far side. Anything else
  // is text; guessing beyond this would be guessing.
  let kind = "text";
  try {
    const url = new URL(body);
    if ((url.protocol === "http:" || url.protocol === "https:") && !/\s/.test(body)) kind = "link";
  } catch { /* not a url, so text */ }

  const button = compose.querySelector("button");
  button.disabled = true;
  bodyInput.value = "";

  try {
    await api("/api/say", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ to: current, body, kind }),
    });
    append({ kind, mine: true, body, extra: "", at: Date.now() });
    loadPeers();
  } catch (err) {
    bodyInput.value = body;
    append({ kind: "event", mine: false, body: `not sent: ${err.message}`, extra: "", at: Date.now() });
  } finally {
    button.disabled = false;
    bodyInput.focus();
  }
});

document.querySelector("#refresh").addEventListener("click", loadPeers);

// What arrives while the page is open. Reconnects on its own, because the bridge restarting should
// not mean a dead tab.
function listen() {
  const events = new EventSource("/api/events");
  events.onmessage = (e) => {
    const m = JSON.parse(e.data);
    if (current) append(m);
    loadPeers();
  };
  events.onerror = () => {
    events.close();
    setTimeout(listen, 2000);
  };
}

loadPeers().catch(() => {});
listen();

const fileInput = document.querySelector("#file");
const dropTarget = document.querySelector("#drop-target");

// Upload goes through XMLHttpRequest rather than fetch because only XHR reports how far a body has
// got. On a large file over a slow link that number is the difference between working and hung.
function upload(to, file, onProgress) {
  return new Promise((resolve, reject) => {
    const form = new FormData();
    form.append("to", to);
    form.append("file", file);

    const req = new XMLHttpRequest();
    req.open("POST", "/api/send");
    req.upload.addEventListener("progress", (e) => {
      if (e.lengthComputable) onProgress(e.loaded / e.total);
    });
    req.addEventListener("load", () => {
      let body = {};
      try { body = JSON.parse(req.responseText); } catch { /* keep the status */ }
      if (req.status >= 200 && req.status < 300) resolve(body);
      else reject(new Error(body.error || req.statusText || "upload failed"));
    });
    req.addEventListener("error", () => reject(new Error("the connection dropped")));
    req.addEventListener("abort", () => reject(new Error("cancelled")));
    req.send(form);
  });
}

function humanSize(n) {
  const units = ["B", "KB", "MB", "GB"];
  let i = 0;
  while (n >= 1024 && i < units.length - 1) { n /= 1024; i++; }
  return `${n < 10 && i > 0 ? n.toFixed(1) : Math.round(n)} ${units[i]}`;
}

// One row per file, updated in place as the bytes go out. Files are sent one at a time: the far end
// writes them into the same inbox, and one connection at a time is what keeps that ordering.
async function sendFiles(files) {
  if (!current) return;

  for (const file of files) {
    const to = current;
    const row = append({ kind: "event", mine: true, body: `${file.name} · 0%`, extra: "", at: Date.now() });

    try {
      await upload(to, file, (fraction) => {
        row.querySelector(".bubble").textContent =
          `${file.name} · ${Math.round(fraction * 100)}%`;
      });
      row.querySelector(".bubble").textContent = `${file.name} · ${humanSize(file.size)} sent`;
      loadPeers();
    } catch (err) {
      row.querySelector(".bubble").textContent = `${file.name} · not sent: ${err.message}`;
    }
  }
}

document.querySelector("#attach").addEventListener("click", () => fileInput.click());
fileInput.addEventListener("change", () => {
  sendFiles([...fileInput.files]);
  fileInput.value = "";
});

// dragover has to be cancelled or the browser navigates to the file instead of handing it over.
let dragDepth = 0;
window.addEventListener("dragover", (e) => e.preventDefault());
window.addEventListener("dragenter", (e) => {
  e.preventDefault();
  if (current && ++dragDepth === 1) dropTarget.hidden = false;
});
window.addEventListener("dragleave", () => {
  if (--dragDepth <= 0) { dragDepth = 0; dropTarget.hidden = true; }
});
window.addEventListener("drop", (e) => {
  e.preventDefault();
  dragDepth = 0;
  dropTarget.hidden = true;
  if (e.dataTransfer?.files?.length) sendFiles([...e.dataTransfer.files]);
});

const termPanel = document.querySelector("#term-panel");
const termBox = document.querySelector("#term");
const termPath = document.querySelector("#term-path");
const termState = document.querySelector("#term-state");

let screen = new Screen(80, 24);
let watching = null;

// The grid is sized from one character, measured from the element that will hold it. Guessing a
// width instead would wrap every line at the wrong column on any font but the one guessed for.
function fit() {
  const probe = document.createElement("span");
  probe.textContent = "M".repeat(50);
  probe.style.cssText = "position:absolute;visibility:hidden;white-space:pre";
  termBox.append(probe);
  const rect = probe.getBoundingClientRect();
  probe.remove();

  const cw = rect.width / 50;
  const ch = rect.height;
  if (!cw || !ch) return;

  const cols = Math.max(20, Math.floor(termBox.clientWidth / cw));
  const rows = Math.max(6, Math.floor(termBox.clientHeight / ch));
  if (cols !== screen.cols || rows !== screen.rows) {
    screen.resize(cols, rows);
    schedulePaint();
  }
}

function stopWatching() {
  if (watching) { watching.close(); watching = null; }
}

function startWatching() {
  if (!current) return;
  stopWatching();

  const path = termPath.value.trim() || "/tty";
  screen = new Screen(screen.cols, screen.rows);
  paint(screen, termBox);
  termState.textContent = "connecting";
  termState.className = "";

  const decoder = new TextDecoder("utf-8");
  const source = new EventSource(`/api/watch/${encodeURIComponent(current)}/${path.replace(/^\/+/, "")}`);

  source.onopen = () => { termState.textContent = "live"; termState.className = "live"; };

  source.onmessage = (e) => {
    const raw = atob(e.data);
    const bytes = new Uint8Array(raw.length);
    for (let i = 0; i < raw.length; i++) bytes[i] = raw.charCodeAt(i);
    // stream:true, because a multi-byte character can be split across two arrivals.
    screen.write(decoder.decode(bytes, { stream: true }));
    schedulePaint();
  };

  source.addEventListener("gone", (e) => {
    termState.textContent = atob(e.data);
    termState.className = "gone";
    stopWatching();
  });

  // EventSource retries on its own, which for a terminal means silently reconnecting to a shell
  // that has already gone. Better to say so and let the watch be started again deliberately.
  source.onerror = () => {
    if (source.readyState === EventSource.CLOSED || termState.textContent === "connecting") {
      termState.textContent = "disconnected";
      termState.className = "gone";
      stopWatching();
    }
  };

  watching = source;
}

document.querySelector("#watch-open").addEventListener("click", () => {
  termPanel.hidden = !termPanel.hidden;
  if (!termPanel.hidden) { fit(); termPath.focus(); } else { stopWatching(); }
});

document.querySelector("#term-go").addEventListener("click", startWatching);
termPath.addEventListener("keydown", (e) => { if (e.key === "Enter") startWatching(); });
document.querySelector("#term-close").addEventListener("click", () => {
  stopWatching();
  termPanel.hidden = true;
});

window.addEventListener("resize", () => { if (!termPanel.hidden) fit(); });

// Output can arrive far faster than a screen refreshes. Painting per chunk would rebuild the grid
// thousands of times a second for a scrolling build log; one paint per frame shows the same thing.
let paintQueued = false;
function schedulePaint() {
  if (paintQueued) return;
  paintQueued = true;
  requestAnimationFrame(() => {
    paintQueued = false;
    paint(screen, termBox);
  });
}
