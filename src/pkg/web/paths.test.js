// Run with `bun test` or `deno test`. Neither is a dependency of drop — this checks the rules the
// page walks paths by, and the page ships whether or not anything here was ever run.

import { under, segments, clean, levelAt, openable } from "./assets/paths.js";

const ok = (cond, why) => { if (!cond) throw new Error(why); };
const eq = (got, want, what = "") => {
  const a = JSON.stringify(got), b = JSON.stringify(want);
  if (a !== b) throw new Error(`${what} got ${a}, want ${b}`);
};

// A tiny runner, so this works under bun and deno without either one's test API.
const tests = [];
const test = (name, fn) => tests.push([name, fn]);

// ---------------------------------------------------------------- prefixes

test("a path is under the root", () => {
  ok(under("/", "/anything/at/all"));
});

test("a path is under its own branch", () => {
  ok(under("/friends", "/friends/chat"));
});

// The failure that matters: a rule or a level must not leak across a name that merely shares a
// prefix, or /friendsonly ends up inside /friends.
test("a prefix does not reach a name that merely starts the same", () => {
  ok(!under("/friends", "/friendsonly"));
  ok(!under("/friends", "/friendsonly/chat"));
});

test("a path is not under itself", () => {
  ok(!under("/friends", "/friends"));
});

test("segments drops the empty parts", () => {
  eq(segments("//friends//chat/"), ["friends", "chat"]);
  eq(segments("/"), []);
});

test("clean rebuilds a path", () => {
  eq(clean("//friends//chat/"), "/friends/chat");
  eq(clean(""), "/");
});

// ---------------------------------------------------------------- levels

const shares = [
  { path: "/friends", kind: "branch", writable: false },
  { path: "/friends/chat", kind: "chat", writable: true },
  { path: "/friends/files", kind: "files", writable: true },
  { path: "/friends/deep/down/here", kind: "chat", writable: true },
  { path: "/work", kind: "branch", writable: false },
  { path: "/work/term", kind: "tty", writable: false },
];

test("the root shows the top of the tree, not everything in it", () => {
  const rows = levelAt(shares, "/");
  eq(rows.map((r) => r.name), ["friends", "work"]);
  ok(rows.every((r) => r.deeper), "both should be walkable");
});

test("a branch shows what is directly under it", () => {
  const rows = levelAt(shares, "/friends");
  eq(rows.map((r) => r.name), ["chat", "files", "deep"]);
});

// The point of the whole exercise: a device may share something four levels down without declaring
// the levels above it, and those still have to be walkable.
test("a level nobody declared is still there to walk into", () => {
  const deep = levelAt(shares, "/friends").find((r) => r.name === "deep");
  eq(deep.kind, "branch", "an undeclared level");
  ok(deep.deeper, "it must be walkable");

  eq(levelAt(shares, "/friends/deep").map((r) => r.name), ["down"]);
  eq(levelAt(shares, "/friends/deep/down").map((r) => r.name), ["here"]);

  const leaf = levelAt(shares, "/friends/deep/down")[0];
  eq(leaf.kind, "chat");
  ok(openable(leaf), "the leaf opens");
});

test("what a path serves survives the walk", () => {
  const chat = levelAt(shares, "/friends").find((r) => r.name === "chat");
  eq(chat.kind, "chat");
  ok(chat.writable, "it takes messages");
  ok(openable(chat), "it opens");
});

test("a branch is walked into, not opened", () => {
  const friends = levelAt(shares, "/")[0];
  ok(!openable(friends), "a branch does not open");
});

// A path that both serves something and has paths under it is real: it opens, and it is walkable.
test("a path can both serve and hold", () => {
  const both = [
    { path: "/logs", kind: "stream", writable: false },
    { path: "/logs/today", kind: "files", writable: true },
  ];

  const row = levelAt(both, "/")[0];
  eq(row.kind, "stream", "it keeps what it serves");
  ok(row.deeper, "and it can still be walked into");
});

test("a level with nothing under it is empty", () => {
  eq(levelAt(shares, "/work/term"), []);
  eq(levelAt(shares, "/nowhere"), []);
});

// Something shared with you at a level you cannot see must not invent a parent.
test("a sibling tree does not leak in", () => {
  eq(levelAt(shares, "/work").map((r) => r.name), ["term"]);
});

// ---------------------------------------------------------------- run

let failed = 0;
for (const [name, fn] of tests) {
  try {
    fn();
    console.log("PASS " + name);
  } catch (err) {
    failed++;
    console.log("FAIL " + name + "\n      " + err.message);
  }
}
console.log(`\n${tests.length - failed}/${tests.length} passed`);
if (failed) {
  throw new Error(`${failed} failing`);
}
