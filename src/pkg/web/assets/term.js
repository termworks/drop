// A terminal screen, enough of one to replay what another device is doing.
//
// This is not a terminal emulator. It never runs a program, never sends a key and implements only
// what output actually uses: a grid, a cursor, erase, scroll and colour. The alternative was to
// embed a real emulator, which costs more than the rest of the binary.

const DEFAULT_FG = -1;
const DEFAULT_BG = -2;

function blankCell() {
  return { ch: " ", fg: DEFAULT_FG, bg: DEFAULT_BG, bold: false, dim: false, italic: false, underline: false, inverse: false };
}

export class Screen {
  constructor(cols = 80, rows = 24) {
    this.resize(cols, rows);
  }

  resize(cols, rows) {
    this.cols = Math.max(1, cols | 0);
    this.rows = Math.max(1, rows | 0);
    this.grid = Array.from({ length: this.rows }, () => Array.from({ length: this.cols }, blankCell));
    this.x = 0;
    this.y = 0;
    this.saved = null;
    this.pending = "";
    this.reset();
  }

  reset() {
    this.fg = DEFAULT_FG;
    this.bg = DEFAULT_BG;
    this.bold = this.dim = this.italic = this.underline = this.inverse = false;
  }

  style() {
    return { fg: this.fg, bg: this.bg, bold: this.bold, dim: this.dim, italic: this.italic, underline: this.underline, inverse: this.inverse };
  }

  clear() {
    for (const row of this.grid) for (let i = 0; i < row.length; i++) row[i] = blankCell();
    this.x = this.y = 0;
  }

  scroll() {
    this.grid.shift();
    this.grid.push(Array.from({ length: this.cols }, blankCell));
  }

  newline() {
    this.y++;
    if (this.y >= this.rows) {
      this.y = this.rows - 1;
      this.scroll();
    }
  }

  put(ch) {
    if (this.x >= this.cols) {
      this.x = 0;
      this.newline();
    }
    this.grid[this.y][this.x] = { ch, ...this.style() };
    this.x++;
  }

  // write takes a chunk that may end mid-escape, so a sequence split across two arrivals is held
  // and finished by the next one rather than printed as text.
  write(text) {
    let s = this.pending + text;
    this.pending = "";

    let i = 0;
    while (i < s.length) {
      const c = s[i];

      if (c === "\x1b") {
        const consumed = this.escape(s, i);
        if (consumed === -1) {
          this.pending = s.slice(i);
          return;
        }
        i += consumed;
        continue;
      }

      i++;
      switch (c) {
        case "\n": this.newline(); break;
        case "\r": this.x = 0; break;
        case "\b": if (this.x > 0) this.x--; break;
        case "\t": this.x = Math.min(this.cols - 1, (this.x + 8) & ~7); break;
        case "\x07": break;
        case "\x0c": this.clear(); break;
        default:
          if (c >= " " && c !== "\x7f") this.put(c);
      }
    }
  }

  // escape returns how many characters the sequence took, or -1 when the chunk ended before it did.
  escape(s, start) {
    const next = s[start + 1];
    if (next === undefined) return -1;

    if (next === "[") {
      let i = start + 2;
      while (i < s.length && /[0-9;?<>!]/.test(s[i])) i++;
      if (i >= s.length) return -1;
      this.csi(s.slice(start + 2, i), s[i]);
      return i - start + 1;
    }

    // An operating-system command sets things like the window title. Nothing here shows one, so it
    // is consumed and dropped; leaving it would print the title into the screen.
    if (next === "]") {
      let i = start + 2;
      while (i < s.length) {
        if (s[i] === "\x07") return i - start + 1;
        if (s[i] === "\x1b" && s[i + 1] === "\\") return i - start + 2;
        i++;
      }
      return -1;
    }

    if (next === "(" || next === ")" || next === "#") {
      if (start + 2 >= s.length) return -1;
      return 3;
    }

    if (next === "M") {
      if (this.y > 0) this.y--;
      return 2;
    }
    if (next === "7") { this.saved = { x: this.x, y: this.y, ...this.style() }; return 2; }
    if (next === "8") { this.restore(); return 2; }
    if (next === "c") { this.clear(); this.reset(); return 2; }
    return 2;
  }

  restore() {
    if (!this.saved) return;
    this.x = this.saved.x;
    this.y = this.saved.y;
    Object.assign(this, {
      fg: this.saved.fg, bg: this.saved.bg, bold: this.saved.bold,
      dim: this.saved.dim, italic: this.saved.italic,
      underline: this.saved.underline, inverse: this.saved.inverse,
    });
  }

  csi(paramText, final) {
    const params = paramText.replace(/^[?<>!]/, "").split(";").map((p) => (p === "" ? 0 : parseInt(p, 10)));
    const n = params[0] || 0;
    const at = (i, fallback) => (params[i] === undefined || params[i] === 0 ? fallback : params[i]);

    switch (final) {
      case "A": this.y = Math.max(0, this.y - at(0, 1)); break;
      case "B": this.y = Math.min(this.rows - 1, this.y + at(0, 1)); break;
      case "C": this.x = Math.min(this.cols - 1, this.x + at(0, 1)); break;
      case "D": this.x = Math.max(0, this.x - at(0, 1)); break;
      case "E": this.x = 0; this.y = Math.min(this.rows - 1, this.y + at(0, 1)); break;
      case "F": this.x = 0; this.y = Math.max(0, this.y - at(0, 1)); break;
      case "G": this.x = Math.min(this.cols - 1, at(0, 1) - 1); break;
      case "d": this.y = Math.min(this.rows - 1, at(0, 1) - 1); break;
      case "H":
      case "f":
        this.y = Math.min(this.rows - 1, at(0, 1) - 1);
        this.x = Math.min(this.cols - 1, at(1, 1) - 1);
        break;
      case "J": this.eraseDisplay(n); break;
      case "K": this.eraseLine(n); break;
      case "L": this.insertLines(at(0, 1)); break;
      case "M": this.deleteLines(at(0, 1)); break;
      case "P": this.deleteChars(at(0, 1)); break;
      case "@": this.insertChars(at(0, 1)); break;
      case "X": this.eraseChars(at(0, 1)); break;
      case "s": this.saved = { x: this.x, y: this.y, ...this.style() }; break;
      case "u": this.restore(); break;
      case "m": this.sgr(params); break;
      default: break;
    }
  }

  eraseDisplay(mode) {
    if (mode === 2 || mode === 3) { this.clear(); return; }
    if (mode === 0) {
      this.eraseLine(0);
      for (let y = this.y + 1; y < this.rows; y++) this.grid[y] = Array.from({ length: this.cols }, blankCell);
    } else if (mode === 1) {
      this.eraseLine(1);
      for (let y = 0; y < this.y; y++) this.grid[y] = Array.from({ length: this.cols }, blankCell);
    }
  }

  eraseLine(mode) {
    const row = this.grid[this.y];
    const from = mode === 0 ? this.x : 0;
    const to = mode === 1 ? this.x + 1 : this.cols;
    for (let i = from; i < to && i < this.cols; i++) row[i] = blankCell();
  }

  eraseChars(count) {
    const row = this.grid[this.y];
    for (let i = this.x; i < Math.min(this.cols, this.x + count); i++) row[i] = blankCell();
  }

  insertLines(count) {
    for (let i = 0; i < count; i++) {
      this.grid.splice(this.y, 0, Array.from({ length: this.cols }, blankCell));
      this.grid.splice(this.rows, 1);
    }
  }

  deleteLines(count) {
    for (let i = 0; i < count; i++) {
      this.grid.splice(this.y, 1);
      this.grid.splice(this.rows - 1, 0, Array.from({ length: this.cols }, blankCell));
    }
  }

  insertChars(count) {
    const row = this.grid[this.y];
    for (let i = 0; i < count; i++) { row.splice(this.x, 0, blankCell()); row.length = this.cols; }
  }

  deleteChars(count) {
    const row = this.grid[this.y];
    for (let i = 0; i < count; i++) { row.splice(this.x, 1); row.push(blankCell()); }
  }

  sgr(params) {
    for (let i = 0; i < params.length; i++) {
      const p = params[i];
      if (p === 0) { this.reset(); continue; }
      if (p === 1) { this.bold = true; continue; }
      if (p === 2) { this.dim = true; continue; }
      if (p === 3) { this.italic = true; continue; }
      if (p === 4) { this.underline = true; continue; }
      if (p === 7) { this.inverse = true; continue; }
      if (p === 22) { this.bold = this.dim = false; continue; }
      if (p === 23) { this.italic = false; continue; }
      if (p === 24) { this.underline = false; continue; }
      if (p === 27) { this.inverse = false; continue; }
      if (p >= 30 && p <= 37) { this.fg = p - 30; continue; }
      if (p >= 90 && p <= 97) { this.fg = p - 90 + 8; continue; }
      if (p >= 40 && p <= 47) { this.bg = p - 40; continue; }
      if (p >= 100 && p <= 107) { this.bg = p - 100 + 8; continue; }
      if (p === 39) { this.fg = DEFAULT_FG; continue; }
      if (p === 49) { this.bg = DEFAULT_BG; continue; }

      // 256-colour and true-colour both carry their arguments in the same parameter list.
      if (p === 38 || p === 48) {
        const target = p === 38 ? "fg" : "bg";
        if (params[i + 1] === 5) { this[target] = params[i + 2] ?? DEFAULT_FG; i += 2; }
        else if (params[i + 1] === 2) { this[target] = `rgb(${params[i + 2] | 0},${params[i + 3] | 0},${params[i + 4] | 0})`; i += 4; }
      }
    }
  }
}

function colour(value, isBg) {
  if (value === DEFAULT_FG) return null;
  if (value === DEFAULT_BG) return null;
  if (typeof value === "string") return value;
  return `var(--t${value})`;
}

function sameRun(a, b) {
  return a.fg === b.fg && a.bg === b.bg && a.bold === b.bold && a.dim === b.dim &&
    a.italic === b.italic && a.underline === b.underline && a.inverse === b.inverse;
}

// paint builds the elements for a screen. Every character goes in as text, never as markup, so
// output from another machine cannot introduce elements here.
export function paint(screen, into) {
  const rows = [];

  for (const row of screen.grid) {
    const line = document.createElement("div");
    line.className = "trow";

    let run = null;
    let text = "";

    const flush = () => {
      if (run === null) return;
      const span = document.createElement("span");
      const fg = run.inverse ? colour(run.bg, true) : colour(run.fg, false);
      const bg = run.inverse ? colour(run.fg, false) : colour(run.bg, true);
      if (fg) span.style.color = fg;
      else if (run.inverse) span.style.color = "var(--term-bg)";
      if (bg) span.style.background = bg;
      else if (run.inverse) span.style.background = "var(--term-fg)";
      if (run.bold) span.style.fontWeight = "700";
      if (run.dim) span.style.opacity = "0.65";
      if (run.italic) span.style.fontStyle = "italic";
      if (run.underline) span.style.textDecoration = "underline";
      span.textContent = text;
      line.append(span);
      text = "";
    };

    for (const cell of row) {
      if (run === null || !sameRun(run, cell)) { flush(); run = cell; }
      text += cell.ch;
    }
    flush();

    rows.push(line);
  }

  into.replaceChildren(...rows);
}
