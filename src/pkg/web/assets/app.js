// The page is organised the way drop is: pick a device, then pick one of the namespaces it serves.
// What a namespace does was decided in that device's config, so nothing here guesses — the list
// comes back from the device and each kind opens the view that fits it.

const peersList = document.querySelector("#peers");
const noPeers = document.querySelector("#no-peers");
const pick = document.querySelector("#pick");
const device = document.querySelector("#device");
const spacesNav = document.querySelector("#spaces");
const spacesNote = document.querySelector("#spaces-note");
const logList = document.querySelector("#log");
const compose = document.querySelector("#compose");
const bodyInput = document.querySelector("#body");
const transfers = document.querySelector("#transfers");
const fileInput = document.querySelector("#file");
const dropTarget = document.querySelector("#drop-target");
const termBox = document.querySelector("#term");
const termState = document.querySelector("#term-state");
const linkForm = document.querySelector("#link-form");
const linkURL = document.querySelector("#link-url");

// Which kind of namespace each view belongs to. A kind with no view here is one the page cannot
// show yet, and it says so rather than opening something misleading.
const VIEWS = {
  chat: "#view-chat",
  files: "#view-files",
  tty: "#view-term",
  stream: "#view-term",
  link: "#view-link",
};

let current = null;   // the device
let space = null;     // the namespace open on it
let spaces = [];

async function api(path, options) {
  const res = await fetch(path, options);
  const body = await res.json().catch(() => ({}));
  if (!res.ok) throw new Error(body.error || res.statusText);
  return body;
}

function when(at) {
  return new Date(at).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
}

// ------------------------------------------------------------------ devices

async function loadPeers() {
  const found = await api("/api/peers");
  peersList.replaceChildren();
  noPeers.hidden = found.length > 0;

  for (const p of found) {
    const li = document.createElement("li");
    const button = document.createElement("button");

    const name = document.createElement("span");
    name.textContent = p.name;
    button.append(name);

    if (p.unread > 0) {
      const badge = document.createElement("span");
      badge.className = "unread";
      badge.textContent = p.unread;
      button.append(badge);
    } else if (!p.paired) {
      const mark = document.createElement("span");
      mark.className = "unpaired";
      mark.textContent = "not paired";
      button.append(mark);
    }

    button.setAttribute("aria-current", String(p.name === current));
    button.addEventListener("click", () => open(p));
    li.append(button);
    peersList.append(li);
  }
}

async function open(peer) {
  stopWatching();
  current = peer.name;
  space = null;

  pick.hidden = true;
  device.hidden = false;
  device.querySelector(".name").textContent = peer.name;
  device.querySelector(".id").textContent = peer.id.slice(0, 16) + "…";

  for (const b of peersList.querySelectorAll("button")) {
    b.setAttribute("aria-current", String(b.textContent.startsWith(peer.name)));
  }

  showView(null);
  spacesNav.replaceChildren();
  spacesNote.hidden = false;
  spacesNote.textContent = `asking ${peer.name} what it serves…`;

  try {
    spaces = await api(`/api/spaces/${encodeURIComponent(peer.name)}`);
  } catch (err) {
    spaces = [];
    spacesNote.textContent = `could not reach ${peer.name}: ${err.message}`;
    return;
  }

  if (!spaces.length) {
    spacesNote.textContent =
      `${peer.name} did not offer anything. It may be an older version, or it has not paired with this device.`;
    return;
  }

  spacesNote.hidden = true;
  drawSpaces();

  // Open something sensible rather than leaving the pane blank: a conversation if there is one,
  // otherwise whatever it listed first.
  enter(spaces.find((s) => s.kind === "chat") || spaces[0]);
}

function drawSpaces() {
  spacesNav.replaceChildren(...spaces.map((s) => {
    const button = document.createElement("button");

    const path = document.createElement("span");
    path.textContent = s.path;

    const kind = document.createElement("span");
    kind.className = "kind";
    kind.textContent = s.kind;

    button.append(path, kind);
    button.setAttribute("aria-current", String(space?.path === s.path));
    button.addEventListener("click", () => enter(s));
    return button;
  }));
}

// enter opens one namespace on the current device.
function enter(s) {
  stopWatching();
  space = s;
  drawSpaces();

  const view = VIEWS[s.kind];
  if (!view) {
    showView(null);
    spacesNote.hidden = false;
    spacesNote.textContent = `${s.path} is a ${s.kind} namespace, which this page cannot show yet.`;
    return;
  }

  spacesNote.hidden = true;
  showView(view);

  if (s.kind === "chat") loadLog();
  if (s.kind === "tty" || s.kind === "stream") startWatching();
}

function showView(which) {
  for (const el of device.querySelectorAll(".view")) el.hidden = true;
  if (which) document.querySelector(which).hidden = false;
}

// ------------------------------------------------------------------ chat

function fillBubble(el, m) {
  // Links are the one thing rendered as markup, and only when the whole body is a URL we parsed
  // ourselves. Everything else is set as text, so a message can never introduce elements.
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
    el.append(tag);
  }

  el.append(document.createTextNode(m.body));
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
  return logList.scrollHeight - logList.scrollTop - logList.clientHeight < 40;
}

function append(m) {
  const row = render(m);
  const stick = atBottom();
  logList.append(row);
  if (stick) logList.scrollTop = logList.scrollHeight;
  return row;
}

async function loadLog() {
  const history = await api(`/api/log/${encodeURIComponent(current)}`);
  logList.replaceChildren(...history.map(render));
  logList.scrollTop = logList.scrollHeight;
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

// ------------------------------------------------------------------ links

linkForm.addEventListener("submit", async (e) => {
  e.preventDefault();
  const body = linkURL.value.trim();
  if (!body || !current) return;

  try {
    new URL(body);
  } catch {
    spacesNote.hidden = false;
    spacesNote.textContent = "that is not a URL";
    return;
  }

  const button = linkForm.querySelector("button");
  button.disabled = true;
  try {
    await api("/api/say", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ to: current, body, kind: "link" }),
    });
    linkURL.value = "";
    spacesNote.hidden = false;
    spacesNote.textContent = `sent to ${current}`;
  } catch (err) {
    spacesNote.hidden = false;
    spacesNote.textContent = `not sent: ${err.message}`;
  } finally {
    button.disabled = false;
  }
});

// ------------------------------------------------------------------ files

// Upload goes through XMLHttpRequest rather than fetch because only XHR reports how far a body has
// got. On a large file over a slow link that number is the difference between working and hung.
function upload(to, at, file, onProgress) {
  return new Promise((resolve, reject) => {
    const form = new FormData();
    form.append("to", to);
    form.append("path", at);
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
    req.send(form);
  });
}

function humanSize(n) {
  const units = ["B", "KB", "MB", "GB"];
  let i = 0;
  while (n >= 1024 && i < units.length - 1) { n /= 1024; i++; }
  return `${n < 10 && i > 0 ? n.toFixed(1) : Math.round(n)} ${units[i]}`;
}

function transferRow(name) {
  const li = document.createElement("li");

  const label = document.createElement("span");
  label.textContent = name;

  const outer = document.createElement("span");
  outer.className = "bar-outer";
  const inner = document.createElement("span");
  inner.className = "bar-inner";
  outer.append(inner);

  const said = document.createElement("span");
  said.className = "said";
  said.textContent = "0%";

  li.append(label, outer, said);
  transfers.prepend(li);
  return { li, inner, said };
}

// Files go one at a time: the far end writes them into one namespace, and one connection at a time
// is what keeps that ordering.
async function sendFiles(files) {
  if (!current || !space) return;

  const at = space.kind === "files" ? space.path : "/inbox";

  for (const file of files) {
    const to = current;
    const row = transferRow(file.name);

    try {
      await upload(to, at, file, (fraction) => {
        row.inner.style.width = `${Math.round(fraction * 100)}%`;
        row.said.textContent = `${Math.round(fraction * 100)}%`;
      });
      row.inner.style.width = "100%";
      row.said.textContent = `${humanSize(file.size)} sent`;
      loadPeers();
    } catch (err) {
      row.li.className = "failed";
      row.said.textContent = err.message;
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
  dragDepth++;
  if (current && dragDepth === 1) dropTarget.hidden = false;
});
window.addEventListener("dragleave", () => {
  dragDepth = Math.max(0, dragDepth - 1);
  if (dragDepth === 0) dropTarget.hidden = true;
});
window.addEventListener("drop", (e) => {
  e.preventDefault();
  dragDepth = 0;
  dropTarget.hidden = true;

  if (!e.dataTransfer?.files?.length || !current) return;

  // Dropping onto any view means the same thing, so switch to where the result is shown.
  const target = spaces.find((s) => s.kind === "files");
  if (target && space?.kind !== "files") enter(target);
  sendFiles([...e.dataTransfer.files]);
});

// ------------------------------------------------------------------ terminal

let watching = null;
let lines = [];

// The screen is built and kept on the other side. What arrives is a list of styled strings, so
// there is nothing here to parse and nothing that could be read as markup: every run goes in as
// text, and the only styles applied are the ones the server named.
function sized(rows) {
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

function applyFrame(frame) {
  if (frame.rows) sized(frame.rows);
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
  if (!current || !space) return;
  stopWatching();

  termBox.replaceChildren();
  lines = [];
  termState.textContent = "connecting";
  termState.className = "";

  const at = space.path.replace(/^\/+/, "");
  const source = new EventSource(`/api/watch/${encodeURIComponent(current)}/${at}`);

  source.onopen = () => { termState.textContent = "live"; termState.className = "live"; };
  source.onmessage = (e) => applyFrame(JSON.parse(e.data));

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

document.querySelector("#term-go").addEventListener("click", startWatching);
document.querySelector("#term-stop").addEventListener("click", () => {
  stopWatching();
  termState.textContent = "stopped";
  termState.className = "";
});

// ------------------------------------------------------------------ live

document.querySelector("#refresh").addEventListener("click", loadPeers);

// What arrives while the page is open. Reconnects on its own, because the bridge restarting should
// not mean a dead tab.
function listen() {
  const events = new EventSource("/api/events");
  events.onmessage = (e) => {
    const m = JSON.parse(e.data);
    if (current && space?.kind === "chat") append(m);
    loadPeers();
  };
  events.onerror = () => {
    events.close();
    setTimeout(listen, 3000);
  };
}

loadPeers();
listen();
