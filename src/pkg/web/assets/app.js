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

let watching = null;
let lines = [];

// The screen is built and kept on the other side. What arrives is a list of styled strings, so
// there is nothing here to parse and nothing that could be read as markup: every run goes in as
// text, and the only styles applied are the ones the server named.
function sized(_cols, rows) {
  lines = Array.from({ length: rows }, () => {
    const el = document.createElement("div");
    el.className = "trow";
    return el;
  });
  termBox.replaceChildren(...lines);
}

function drawLine(el, runs) {
  if (!runs || !runs.length) {
    el.replaceChildren();
    return;
  }

  el.replaceChildren(...runs.map((r) => {
    const span = document.createElement("span");
    span.textContent = r.t;
    if (r.f) span.style.color = r.f;
    if (r.b) span.style.background = r.b;
    if (r.o) span.style.fontWeight = "700";
    if (r.d) span.style.opacity = "0.65";
    if (r.i) span.style.fontStyle = "italic";
    if (r.u) span.style.textDecoration = "underline";
    return span;
  }));
}

function apply(frame) {
  if (frame.cols && frame.rows) sized(frame.cols, frame.rows);
  if (!frame.lines) return;

  for (const [at, runs] of Object.entries(frame.lines)) {
    const el = lines[Number(at)];
    if (el) drawLine(el, runs);
  }
}

function stopWatching() {
  if (watching) { watching.close(); watching = null; }
}

function startWatching() {
  if (!current) return;
  stopWatching();

  const path = termPath.value.trim() || "/tty";
  termBox.replaceChildren();
  lines = [];
  termState.textContent = "connecting";
  termState.className = "";

  const source = new EventSource(`/api/watch/${encodeURIComponent(current)}/${path.replace(/^\/+/, "")}`);

  source.onopen = () => { termState.textContent = "live"; termState.className = "live"; };
  source.onmessage = (e) => apply(JSON.parse(e.data));

  source.addEventListener("gone", (e) => {
    termState.textContent = JSON.parse(e.data);
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
  if (!termPanel.hidden) termPath.focus(); else stopWatching();
});

document.querySelector("#term-go").addEventListener("click", startWatching);
termPath.addEventListener("keydown", (e) => { if (e.key === "Enter") startWatching(); });
document.querySelector("#term-close").addEventListener("click", () => {
  stopWatching();
  termPanel.hidden = true;
});
