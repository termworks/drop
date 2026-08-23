// Walking a device's paths.
//
// A device may share /friends/deep/down/here without declaring anything between, so the levels in
// the middle are worked out rather than served: they exist because something below them does.
//
// Kept apart from the page because it is the only part with rules worth getting wrong, and this way
// it can be checked without a browser.

// under reports whether a path sits below a prefix, on segment boundaries so /friendsonly is not
// read as being inside /friends.
export function under(prefix, path) {
  if (prefix === "/") return true;
  return path.startsWith(prefix.replace(/\/+$/, "") + "/");
}

export function segments(path) {
  return path.split("/").filter(Boolean);
}

export function clean(path) {
  const parts = segments(path);
  return parts.length ? "/" + parts.join("/") : "/";
}

// levelAt is what sits directly under a prefix, and nothing further down: a device with twenty paths
// under four branches is four rows, not twenty.
export function levelAt(shares, prefix) {
  prefix = clean(prefix);

  const depth = segments(prefix).length;
  const at = new Map();

  for (const share of shares) {
    if (!under(prefix, share.path)) continue;

    const parts = segments(share.path);
    if (parts.length <= depth) continue;

    const name = parts[depth];
    const path = "/" + parts.slice(0, depth + 1).join("/");

    const row = at.get(name) || { name, path, kind: "branch", writable: false, deeper: false };

    if (path === share.path) {
      // This is the thing itself, so it brings what it actually serves.
      row.kind = share.kind || "branch";
      row.writable = Boolean(share.writable);
    } else {
      // Something below it, so it can be walked into whatever else it turns out to be.
      row.deeper = true;
    }

    at.set(name, row);
  }
  return [...at.values()];
}

// openable says a row is something to open rather than walk into.
export function openable(row) {
  return row.kind !== "branch";
}
