# How drop finds things

drop has one idea underneath it, and everything else is a consequence:

> **A device's address is its public key.**

Not an IP, not a hostname, not an account on a server. The key never changes, so
pairing two devices is a one-time act and reaching each other afterwards is the
network's problem, not yours.

This document is how that problem gets solved.

---

## 1. The idea, and the trouble with it

A drop node generates an ed25519 keypair once and keeps it forever:

```
~/.config/drop/identity          the private key, 0600
        │
        └─ public half ──► 7b9773d9686b7fd24dcbe88c5a101401ab1f7fbb…
                           this is the address. iroh calls it an endpoint id.
```

That address is **unforgeable**: to answer to it you must hold the private key,
and the QUIC handshake proves it. Nobody can pretend to be your laptop.

It is also **completely unroutable**. No router on earth knows what to do with
a public key. So every connection has two halves:

```
    who                              where
  ────────                        ──────────
  endpoint id       ────────►     192.168.1.157:47777
  (never changes)   resolution    (changes constantly)
```

The endpoint id is permanent and useless for routing. The address is routable
and temporary. **The entire P2P layer exists to turn the left side into the
right side**, over and over, as networks change.

---

## 2. The resolution ladder

drop tries three things, cheapest first — `reachAt` in `src/cmd/dial.go`:

```
  drop to laptop/inbox report.pdf
            │
            ▼
  ┌──────────────────────────────────────────────────────────────┐
  │ 1. the book       where it was last time                     │
  │                   written down at pairing, costs nothing     │
  │                   wrong the moment the device moves          │
  └──────────────────────────────────────────────────────────────┘
            │ also try
            ▼
  ┌──────────────────────────────────────────────────────────────┐
  │ 2. this wire      multicast on the LAN, ~5 seconds           │
  │                   no internet, no relay, no third party      │
  │                   authoritative when it answers              │
  └──────────────────────────────────────────────────────────────┘
            │ silence
            ▼
  ┌──────────────────────────────────────────────────────────────┐
  │ 3. the rendezvous ask a pkarr relay where it is now          │
  │                   off unless drop.rendezvous = true          │
  │                   the only step that talks to a stranger     │
  └──────────────────────────────────────────────────────────────┘
```

The order matters in both directions. A remembered address is tried first
because it is free, but it is never trusted — it is exactly what goes stale
when a laptop leaves the building. The wire overrides it. And **rung 3 only
runs when rung 2 found nothing**: a device standing next to you must not cause
a lookup to a third party, and must not have its traffic dragged through a
relay when a direct address was sitting right there.

A laptop on your desk never leaves rung 2.

---

## 3. Rung 2 — the wire

Every node announces itself on the local network and listens for others doing
the same. `src/pkg/discovery/lan.go`:

```
   192.168.1.10                                    192.168.1.157
  ┌─────────────┐                                 ┌─────────────┐
  │  laptop     │──── "drop-lan-1 7b9773d9…" ────►│ workstation │
  │             │◄─── "drop-lan-1 e88c42df…" ─────│             │
  └─────────────┘      239.255.77.88:47800        └─────────────┘
                        (multicast, no server)
```

Announcements repeat every 2 seconds rather than firing once, so a node that
starts later still hears one; a lookup listens for 5 seconds, which always
spans at least two. An entry goes stale after 30 seconds of silence.

Instant, works with the internet unplugged, needs no relay and no
configuration. It is also strictly LAN-only: multicast does not cross routers.
This is why `drop peers` finds your own machines in a second, and why it finds
nothing when you are on hotel wifi and the other device is at home.

### Why this is drop's own code and not mDNS

drop used to use a shared mDNS library on 5353. It did not work here, and the
reason is worth writing down because it looks like nothing.

The listener had `SO_REUSEADDR` **and** `SO_REUSEPORT` set, which is the
reflexive pair to reach for when a port is already busy — and `avahi-daemon`
holds 5353 on most Linux desktops. But `SO_REUSEPORT` puts every socket on the
port into a **load-balancing group**: the kernel hands each arriving datagram
to exactly one member. For a multicast listener that is precisely backwards.
avahi and drop were in the same group, and avahi was getting the packets.

```
   SO_REUSEADDR + SO_REUSEPORT                SO_REUSEADDR only
   ───────────────────────────                ─────────────────
   packet ──► [ kernel picks one ]            packet ──► every listener
                    │                                        │
              avahi gets it                            drop gets it
              drop gets 0                              6 packets in 3s
```

Same code, one socket option apart, A/B tested: **0 packets against 6**. drop
now runs its own group and port so it is never in a group with anything else,
and `reuseAddr` in `lan.go` sets `SO_REUSEADDR` alone, with a comment saying
why so nobody helpfully adds the other one back.

---

## 4. Rung 3 — the rendezvous, and the privacy problem

Off by default. It writes to a relay you do not own, and that is not something
to start doing on someone's behalf. `drop.rendezvous = true` turns it on.

### What it is built on

iroh can publish a signed record — a **pkarr** packet — to a relay over plain
HTTP, keyed by an ed25519 public key, and read it back:

```
   PUT  <relay>/<key>   signed packet: "this key is at these addresses"
   GET  <relay>/<key>   ──► the packet, signature checked against the key
```

The signature means the relay cannot forge or tamper with a record. It can
still read every record it holds, and it knows which keys exist. That is the
part the design has to deal with.

### The obvious design, and why it is wrong

The naive thing is to publish under your own endpoint id:

```
   key = 7b9773d9686b7fd24dcbe88c5a101401ab1f7fbb…      ◄── DON'T
```

It works, and it is what iroh does by default. It is also a
**presence-tracking beacon**. Endpoint ids are public — you hand them out to
pair. Anyone holding yours could poll that key forever and build a log of
exactly when your laptop is on, from anywhere in the world, without ever
talking to you. Worse, every record you publish is filed under the same key, so
all of them are visibly the same machine.

That is a surveillance surface bolted to a file-sharing tool.

### What drop does instead

The record is published under a **throwaway identity derived from the secret
the two devices established when they paired**, and it rotates:

```
   identity = ed25519( HKDF-SHA256( ikm  = pair secret,
                                    salt = publisher's endpoint id,
                                    info = "drop rendezvous v1" ‖ epoch ) )
                                           ╰────────┬────────╯   ╰──┬──╯
                              only the two of us can compute this   hourly
```

Both sides can compute it; nobody else can, because it takes the pair secret.
Four properties fall out:

| | |
| --- | --- |
| **An endpoint id reveals nothing.** | Knowing a device's address does not let you compute where to look for it. |
| **Records are unlinkable.** | A device paired with three others publishes three unrelated records. Nothing ties them together, or to the device. |
| **Identities rotate hourly.** | Learning one buys an hour, not a permanent handle. |
| **Only relay addresses are published.** | Your IP does not go into a record a stranger holds. |

The publisher's id is in the salt for a blunt reason: without it both ends of a
pair derive the *same* identity, and whichever publishes second overwrites the
other's address with its own. Both devices then become unreachable. There is a
test for exactly that.

### The epoch boundary

Naively you publish under the current hour and look under the current hour.
That breaks every time the clock rolls over:

```
        11:59                 12:00
          │                     │
  publisher ──────────────────► writes under E12
  resolver  ──────────────────► reads   E13
                                         ▲
                                 no overlap. they never meet.
```

So publishing covers **this hour and the next**, and resolving covers **this
hour and the last**:

```
   publish:        [ E ][ E+1 ]
   resolve:  [ E-1 ][ E ]
                    ╰─┬─╯
              always overlaps, either side of the boundary
```

A test asserts the windows intersect one second before the hour turns and one
second after. This is the second time this exact bug has been written in this
codebase; the first version had the windows sliding in opposite directions and
they never met at all.

---

## 5. Pairing — where the secret comes from

The secret has to be established over a channel that is already authenticated,
or an eavesdropper gets it and the whole scheme collapses. drop pairs over an
iroh connection, which QUIC has already encrypted and **mutually
authenticated** — both ends have proven they hold their private keys before a
byte of pairing traffic flows.

```
  device A                                        device B
  ────────                                        ────────
  drop pair
     │
     │  prints a ticket:
     │    7b9773d9…#fqdv-q64c-ebfl#192.168.1.157:47901
     │    ╰── id ──╯╰── one-time code ─╯╰── where I am ──╯
     │
     └──── carried by hand, or any channel you trust ────► drop pair <ticket>
                                                                  │
     │  ═════ ALPN drop/pair/1: encrypted, both ends proven ══════╪══
     │                                                            │
     ├──── nonce_A (32 random bytes) ────────────────────────────►│
     │◄─── nonce_B (32 random bytes) ─────────────────────────────┤
     ▼                                                            ▼
   secret = HKDF-SHA256(                              the same computation,
       ikm  = nonce_lo ‖ nonce_hi,                    on the same inputs,
       salt = id_lo ‖ id_hi,                          gives the same 32 bytes
       info = "drop pair v1")
```

**`lo` and `hi` are the two endpoint ids sorted.** Each side sees the exchange
from the opposite direction — its own nonce is "mine", the other is "theirs" —
so without a canonical order the two would mix the inputs in opposite orders
and derive different secrets. Sorting by id gives both the same view.

The code proves the far end was actually invited: it never crosses the network
in the clear, only as an HMAC over it. Both sides then store the secret in
`peers.json`, mode 0600. Verified in testing: after pairing, both files held
**byte-identical secrets**.

There is no rendezvous involved in pairing. The ticket carries the address, so
first contact needs nothing running anywhere else.

---

## 6. NAT, and when a relay is unavoidable

Your laptop has `192.168.1.157`. So do a hundred million other laptops. Two
machines each behind their own NAT cannot dial each other, because neither has
an address the other can use.

```
   laptop                  NAT A          internet          NAT B         desktop
  192.168.1.157  ────────►  │                                │  ◄──────  10.0.0.4
                            │                                │
                     137.224.252.107                  81.4.109.22
                            │                                │
                            └──── dial? ──►  ✗  ◄── dial? ───┘
                              neither side can start it
```

When `drop.rendezvous` is on, drop enables iroh's relays and net report. A
relay is a publicly reachable third party that will carry bytes for two nodes
that cannot reach each other, and coordinate an attempt to escape it:

```
                        ┌──────────┐
                        │  relay   │
                        └────┬─────┘
              reservation ┌──┴───┐ dial via relay
                     ┌────▼──┐ ┌─▼──────┐
                     │ laptop│ │desktop │
                     └───────┘ └────────┘

   1. both reach the relay outbound, which every NAT allows
   2. bytes flow through it, end-to-end encrypted the whole way
   3. both try a simultaneous direct dial; if it lands, the relay is dropped
```

The relay forwards **encrypted** bytes. It sees that traffic exists and roughly
how much; it cannot read any of it, because the QUIC session is end-to-end
between the two nodes. Combined with the derived rendezvous identity, what a
relay operator can observe is: some key it cannot attribute is reachable, and
some traffic is passing. Not who, and not what.

Set `drop.relays` to your own if you would rather not use the defaults.

> **Honest status:** the rendezvous round trip is proven — a record published
> under a derived identity, read back from the real relay, carrying a relay
> address and no IP. Two nodes actually meeting *only* through it, across
> different NATs, has not been exercised here; both test machines are on one
> wire, where rung 2 answers first by design.

---

## 7. The whole thing, end to end

`drop to laptop/inbox report.pdf`, on a machine that has never met the
laptop's current network:

```
  ┌────────────────────────────────────────────────────────────────────┐
  │ 1. RESOLVE THE NAME                                                │
  │    "laptop" ──► peers.json ──► endpoint id + shared secret         │
  └────────────────────────────────────────────────────────────────────┘
                              │
  ┌────────────────────────────────────────────────────────────────────┐
  │ 2. FIND IT                                                         │
  │    remembered address ............................... stale        │
  │    this wire, 5s .................................... nothing      │
  │    rendezvous: GET <relay>/<derived id for this hour>              │
  │                and for the hour before                             │
  │                ──► relay:https://euc1-1.relay.…                    │
  └────────────────────────────────────────────────────────────────────┘
                              │
  ┌────────────────────────────────────────────────────────────────────┐
  │ 3. CONNECT                                                         │
  │    QUIC, ALPN drop/session/1                                       │
  │    both ends prove their keys                                      │
  │    ✗ wrong key ──► refused. impersonation is impossible            │
  └────────────────────────────────────────────────────────────────────┘
                              │
  ┌────────────────────────────────────────────────────────────────────┐
  │ 4. TALK                                                            │
  │    open a namespace, and the archetype it names answers:           │
  │                        ┌─ share    (pushed in, once)               │
  │                        ├─ files    (a folder, walked)              │
  │                        ├─ stream   (endless, one direction)        │
  │                        ├─ tty      (a terminal, fanned out)        │
  │                        ├─ chat     (messages)                      │
  │                        └─ link     (open it over there)            │
  │    many streams multiplexed over the one connection                │
  └────────────────────────────────────────────────────────────────────┘
```

Step 4 is worth dwelling on: because QUIC multiplexes, a file transfer, a chat
message and a terminal stream all run **concurrently over one connection**,
each independently flow-controlled. That is why "everything is a conversation"
is cheap rather than expensive.

An ALPN is chosen per **connection**, not per stream — `drop/session/1` for
ordinary work, `drop/pair/1` for pairing, `drop/hello/1` for a liveness check.

---

## 8. Namespaces, so this is not a pile of subcommands

A device has one identity and serves named paths under it. Each path is a
**namespace**, and what it *means* is its **archetype** — declared in the
config, not baked into a command:

```
   laptop
     ├── /inbox          share    ~/Downloads          things land here
     ├── /inbox/photos   share    ~/Pictures/drop
     ├── /papers         files    ~/papers             read-only, walked
     ├── /logs           stream   journalctl -f
     ├── /term           tty      /bin/sh, read-only
     ├── /chat           chat
     └── /open           link     xdg-open
```

`drop to laptop/inbox file.pdf` and `drop to laptop/term` are the same command;
what happens is whatever the far side said that path is. Lookup is
longest-declared-prefix on segment boundaries, so `/inbox/photos` wins over
`/inbox` without `/inbox` having to know it exists.

Two namespaces of one archetype are two instances of the same thing, with
separate settings, rules and state — `/inbox` and `/inbox/photos` above. The
layer that resolves a path and checks a rule knows only which archetype a
namespace names, never what that name means; every one of them is an
implementation of `arch.Archetype` and a line in `src/cmd/archetypes.go`.

`share` and `files` both point at a directory and are opposites. A `share` is
pushed into and never read; a `files` is listed and read, and written to only
when the mount says `writable = true`, which also means deleted from.

A `tty` namespace serves **one** terminal fanned out to every watcher, not one
shell per caller — the terminal belongs to the namespace, and joining hands over
the screen as it stands so a late watcher sees the same picture.

---

## 9. What has to be running

The uncomfortable requirement, true of any system like this:

```
   drop to laptop/inbox file.pdf
                       │
                       └─► only works if something on the laptop is
                           online and reachable
```

That is `drop serve`, installed as a user service:

```
   install -m 0644 misc/drop.service ~/.config/systemd/user/
   systemctl --user enable --now drop
```

Without it, both sides have to be running drop at the same moment — which is
croc's model, not "pair once and reach forever". With `drop.rendezvous = true`
the same process republishes its address every 5 minutes, because a record
that stops being refreshed stops being findable.

`drop` with no arguments also holds the node open, so a terminal on this machine keeps it
reachable for as long as the page is.

---

## 10. Failure modes, and what they look like

| symptom | what is actually wrong |
| --- | --- |
| `no peers on this network` | Rung 2 only; the other device is on a different subnet. Expected off-LAN. |
| found it, but the connection fails | The remembered address is stale and nothing newer was found. Turn on the rendezvous, or re-pair. |
| the rendezvous returns nothing | The far end is not publishing — it has `drop.rendezvous` off, or nothing is running there. |
| `pkarr relay returned status 404` | No record under that identity yet. Normal for a few seconds after a node starts. |
| `declined: not paired with you` | No shared secret, so no private rendezvous exists either. `drop pair` once. |
| a device is found but everything is slow | Traffic is crossing a relay because no direct path was found. |

---

## 11. Where this lives in the code

```
src/pkg/node/identity.go        the keypair, and therefore the address
src/pkg/node/host.go            the endpoint: ALPNs, port, relays
src/pkg/node/settings.go        what the config is allowed to change
src/pkg/discovery/lan.go        rung 2, and the SO_REUSEPORT story
src/pkg/rendezvous/derive.go    rung 3: the derived identity and the windows
src/pkg/rendezvous/service.go   publishing one record per pair, and resolving
src/pkg/proto/pair.go           the exchange the shared secret comes from
src/pkg/proto/session.go        what a connection can be asked to do
src/pkg/ns/                     namespaces: paths, access rules, the mount table
src/pkg/arch/                   archetypes: what a namespace means, one package each
src/pkg/term/screen.go          the terminal grid another device is drawing on
src/pkg/term/frame.go           what changed since a watcher was last told
src/cmd/dial.go                 the ladder itself, in twenty lines
```

`dial.go` is the shortest useful thing to read: `reachAt` is the whole strategy
with nothing else in the way.
