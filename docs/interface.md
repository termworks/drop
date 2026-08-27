# The interface

`drop` with no arguments is a full-screen interface. It reaches the same node over the same protocol
as the command line — everything one can do, the other can.

```console
drop path ls beta     the command line: what beta shares with you
drop                  the interface: pick a person, then a machine, then a path
```

## People, then machines, then paths

The first screen is **users**, not devices. A user is what everything else is written against:
access rules name people, trust belongs to people, and a machine somebody buys next week is already
covered by a rule that names them.

```
╭─ users ──────────────────────────────────────────────────────────╮
│   ◈ me                                                2 machines │
│   you — every machine your user key has signed                   │
│   access = { "me" }   ·   2 reachable                            │
│   ★ bob                                               2 machines │
│   a person you decided to trust                                  │
│   access = { "bob" }                                             │
│   ◌ anon                                             one machine │
│   machines paired on their own, belonging to nobody              │
╰──────────────────────────────────────────────────────────────────╯
```

You are a user like any other: `me` holds this machine and every other one your user key has signed.
A device paired with `--machine` belongs to nobody, and nobody is a user called **anon** — so the
screen stays one kind of thing rather than three.

Enter a user to see their machines, enter a machine to see what it shares. Each path is drawn by its
archetype's own glyph and description, which is what `Note` in [namespaces](namespaces.md) is for: a
namespace of a kind this build has never heard of still gets a row.

## Managing somebody

`m` on somebody in the users list opens the screen for them — who they are, whether they are
trusted, and every path you have granted or refused them.

```
╭─ bob  ·  who they are, and what you have given them ─────────────────╮
│ ╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌ WHO THEY ARE ╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌ │
│   · bob                                                              │
│   a person, with 2 machines                                          │
│ ╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌ TRUST ╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌ │
│   ✓ trusted                                                          │
│ ╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌ WHAT THEY MAY REACH ╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌ │
│   ✓ /work                                                            │
╰──────────────────────────────────────────────────────────────────────╯
  t  trust   x  revoke   f  forget   esc  back
```

It is a separate screen on purpose. The list answers *what can I open*, which you ask constantly;
this answers *who is this and what have I given them*, which you ask rarely. Putting both in one
list made neither readable.

## Listings are filtered, not refused

You are shown what you could reach and what you could ask for, and told nothing about the rest. A
path you may see but not open is [a door with a bell on it](access.md); a path guarded by a password
is in no listing at all, because nobody offers a secret to ask what exists.

## Keys

`?` shows them. There is no permanent shortcut line and no box around the interface — the screen is
for what you are looking at.

The one worth knowing here is what happens with a terminal: `i` gives it the keyboard and **ctrl+]**
takes it back. While it has the keyboard it gets *every* key there is, `esc` and `q` included,
because half a keyboard is not a terminal. The panel says `· typing` so you can see where your keys
are going. See [sharing a terminal](terminal.md).

## Where it lives

`src/pkg/tui/`. What a peer supplies is cleaned before it is drawn — see
[hardening](security.md), since a listing is exactly the place where somebody else's bytes meet a
terminal.
