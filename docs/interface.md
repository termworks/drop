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

## Managing people, machines, and who opens what

Every screen says along its bottom line what can be done on it, and **space** lays every action out
as a menu to pick from — so nothing here has to be known to be found.

| where | what you can do |
|---|---|
| users | `a` add a machine of yours (shows a code), `c` join your machines with a code, `p` pair with somebody, `t` take their code, `m` manage somebody, `n` rename them, `x` remove them |
| your machines | `n` rename one, `x` take it out of your machines, `a` add one |
| somebody's machines | `n` rename one, `x` forget it, `t` trust them, `m` manage them |
| paths on a machine of yours | `w` who may open it — this machine or any other of yours |

Taking a machine out of yours is a mark every machine of yours takes from the others: from then on
all of them turn it away as a stranger, whatever badge it still wears, and `drop machine add` puts
it back.

**Who may open a path** is the same ladder the phone shows — *Only me*, *Trusted*, *Paired*,
*Public* — and whether others may see the path and ask for it, then everybody who asked and
everybody who can be let in or kept out by name:

```
╭─ /work on this machine  ·  who may open it ──────────────────────────╮
│ ╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌ WHO MAY OPEN IT ╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌ │
│   · Only me                                                          │
│   ✓ Trusted                                                          │
│   · Paired                                                           │
│   · Public                                                           │
│   · Others may see it and ask                                        │
│ ╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌ BY NAME ╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌ │
│   ✓ bob                                                              │
│   let in by name                                                     │
╰──────────────────────────────────────────────────────────────────────╯
 enter choose · a let bob in · x keep bob out · d leave to the step
```

**Managing somebody** is who they are, whether you trust them, and what they may open on *every*
machine of yours, each asked and answering for itself — changed from the same screen with `a`, `x`
and `d`:

```
╭─ bob  ·  who they are, and what they may open ───────────────────────╮
│ ╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌ TRUST ╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌ │
│   ✓ trusted                                                          │
│ ╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌ WHAT THEY MAY OPEN ╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌ │
│ ╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌ ON THIS MACHINE ╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌ │
│   ✓ /work                                                            │
│   opens · Trusted                                                    │
│ ╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌ ON LAPTOP ╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌ │
│   ✗ /keys                                                            │
│   shut · kept out by name                                            │
╰──────────────────────────────────────────────────────────────────────╯
 t stop trusting them · a let in, on laptop · x keep out · d leave to the step
```

## Listings are filtered, not refused

You are shown what you could reach and what you could ask for, and told nothing about the rest. A
path you may see but not open is [a door with a bell on it](access.md); a path guarded by a password
is in no listing at all, because nobody offers a secret to ask what exists.

## Keys

The line along the bottom shows as many of the keys as fit, `?` shows all of them, and **space** lays
the actions out to pick from.

The one worth knowing here is what happens with a terminal: `i` gives it the keyboard and **ctrl+]**
takes it back. While it has the keyboard it gets *every* key there is, `esc` and `q` included,
because half a keyboard is not a terminal. The panel says `· typing` so you can see where your keys
are going. See [sharing a terminal](terminal.md).

## Beside the daemon

With `drop serve` running, the interface is a view onto it and has no endpoint of its own:
everything it reaches, it reaches through the daemon's connections, and what arrives is announced
to it over a socket on this machine. A second endpoint under the same identity — even one that only
lived for a moment — would announce itself on the wire and take the relay's route to this
identity, and the far end's answers to the daemon would go somewhere nobody is reading.

## Driven from another terminal

Every interface listens on a socket of its own, readable only by this account, and `drop tui` in
another terminal reads its screen and presses its keys. See [the command line](cli.md).

## Where it lives

`src/pkg/tui/`. What a peer supplies is cleaned before it is drawn — see
[hardening](security.md), since a listing is exactly the place where somebody else's bytes meet a
terminal.
