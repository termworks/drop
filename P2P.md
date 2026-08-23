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
        └─ public half ──► 12D3KooWCK6VkpAm8xUbhLmepys1aajJffzSfBzAvmhikMsTFctA
                           this is the address. it is called a peer id.
```

That address is **unforgeable**: to answer to it you must hold the private key,
and the connection handshake proves it. Nobody can pretend to be your laptop.

It is also **completely unroutable**. No router on earth knows what to do with
a public key. So every connection has two halves:

```
    who                              where
  ────────                        ──────────
  peer id           ────────►     /ip4/137.224.252.107/udp/44061/quic-v1
  (never changes)   resolution    (changes constantly)
```

The peer id is permanent and useless for routing. The address is routable and
temporary. **The entire P2P layer exists to turn the left side into the right
side**, over and over, as networks change.

---

## 2. The resolution ladder

drop tries three things, cheapest first. `src/cmd/locate.go`:

```
  drop send file.txt --to laptop
            │
            ▼
  ┌──────────────────────────────────────────────────────────────┐
  │ 1. mDNS          same wire?                                  │
  │                  multicast on the LAN, ~3 seconds            │
  │                  no internet, no DHT, no infrastructure      │
  └──────────────────────────────────────────────────────────────┘
            │ not found
            ▼
  ┌──────────────────────────────────────────────────────────────┐
  │ 2. DHT rendezvous                                            │
  │                  join the global Kademlia DHT                │
  │                  look under a key only we two can compute    │
  │                  returns the peer's current addresses        │
  └──────────────────────────────────────────────────────────────┘
            │ found addresses, but they are behind a NAT
            ▼
  ┌──────────────────────────────────────────────────────────────┐
  │ 3. relay + hole punch                                        │
  │                  dial via a circuit relay, then try to       │
  │                  upgrade to a direct link (DCUtR)            │
  └──────────────────────────────────────────────────────────────┘
```

Each rung is slower and needs more of the world to be working than the one
above it. A laptop on your desk never leaves rung 1.

---

## 3. Rung 1 — mDNS, the same-wire shortcut

Multicast DNS. Every drop node shouts "I am here" on the local network under
the service tag `drop`, and listens for others doing the same.

```
   192.168.1.10                                    192.168.1.157
  ┌─────────────┐                                 ┌─────────────┐
  │  laptop     │──── "drop: 12D3KooWC3tn…" ─────►│ workstation │
  │             │◄─── "drop: 12D3KooWCK6V…" ──────│             │
  └─────────────┘        224.0.0.251:5353         └─────────────┘
                         (multicast, no server)
```

Instant, works with the internet unplugged, and needs no DHT, no relay and no
bootstrap node. It is also strictly LAN-only: multicast does not cross routers.

This is why `drop peers` finds your own machines in a second, and why it finds
nothing when you are on hotel wifi and the other device is at home.

---

## 4. Rung 2 — the DHT, and the privacy problem

### What a DHT is, briefly

A distributed hash table is a global key→value store with no server. Millions
of nodes each hold a small slice of the keyspace. To publish, you find the
nodes numerically closest to your key and hand them the value; to look up, you
walk the network toward that key until you reach them.

drop joins the **public libp2p/IPFS DHT** — the same one IPFS uses — so there
are already hundreds of thousands of nodes doing the routing. In testing, a
drop node had **167 peers in its routing table within a minute**.

```
        the keyspace, as a ring
             ┌─────────┐
        ┌────┤ 0x0000… ├────┐
        │    └─────────┘    │
   ┌────┴────┐         ┌────┴────┐
   │ nodes   │         │ nodes   │      a key lands somewhere on the ring;
   │ near A  │         │ near B  │      whoever is closest stores its value
   └────┬────┘         └────┬────┘
        │    ┌─────────┐    │
        └────┤ 0xFFFF… ├────┘
             └─────────┘

   publish:  "the value for key K is: my current addresses"
   lookup:   "who has the value for key K?"  ──►  addresses
```

### The obvious design, and why it is wrong

The naive thing is to announce yourself under your own peer id:

```
   key = "drop:node:12D3KooWCK6Vkp…"      ◄── DON'T
```

It works. It is also a **presence-tracking beacon**. Peer ids are public — you
hand them out to pair. Anyone holding yours could poll that key forever and
build a log of exactly when your laptop is on, from anywhere in the world,
without ever talking to you.

That is a surveillance surface bolted to a file-sharing tool.

### What drop does instead

The rendezvous key is derived from a **secret shared only by the two paired
devices**, and it rotates:

```
   key = "drop:pair:" + HMAC-SHA256(shared_secret, "drop:pair:v1:" + hour)
                        ╰────┬────╯                              ╰──┬──╯
                    only the two of us know this        changes every hour
```

Three properties fall out of that:

| | |
| --- | --- |
| **A peer id reveals nothing.** | Knowing the address does not let you compute where to look. |
| **Keys rotate hourly.** | Learning one key buys an hour, not a permanent handle. |
| **Secrets are per-pair.** | Your phone cannot observe when you are reachable by your work machine. That last one is why the secret is per-pair rather than one per device. |

### The hour-boundary bug

Naively you announce under the current hour and look under the current hour.
That breaks every time the clock rolls over:

```
        11:59                 12:00
          │                     │
  announcer ──────────────────► publishes under W12, W13
  looker    ──────────────────► searches   W11, W10
                                            ▲
                                    no overlap. they never meet.
```

So announcing covers **three** windows and lookup covers two:

```
   announce:  [ W-1 ][ W ][ W+1 ]
   lookup:         [ W-1 ][ W ]
                   ╰──────┬───╯
                    always overlaps, for any skew up to ±1 hour
```

A test asserts this holds at 0, ±½ and ±1 window of skew. It failed when
written, which is how the bug was found.

---

## 5. Pairing — where the secret comes from

The secret has to be established over a channel that is already authenticated,
or an eavesdropper gets it and the whole scheme collapses. drop pairs over a
libp2p stream, which Noise has already encrypted and **mutually authenticated**
— both ends have proven they hold their private keys before a byte of pairing
traffic flows.

```
  device A                                        device B
  ────────                                        ────────
  drop pair
  code: qxwo-e62y-bzfs   ◄── 60 bits of entropy, one-time
     │
     │ announces under sha256("drop:code:v1:" + code)
     ▼
  [ DHT ]                                          drop pair qxwo-e62y-bzfs
     │                                                  │
     │◄────────────── looks up the same hash ───────────┘
     │
     │  ══════ libp2p stream: encrypted + both ends authenticated ══════
     │
     ├──── nonce_A (32 random bytes) ─────────────────►
     ◄──── nonce_B (32 random bytes) ──────────────────┤
     │                                                  │
     ▼                                                  ▼
   secret = HKDF-SHA256(                        the same computation,
       ikm  = nonce_lo ‖ nonce_hi,              on the same inputs,
       salt = peerid_lo ‖ peerid_hi,            gives the same 32 bytes
       info = "drop pair v1")
```

**`lo` and `hi` are the two peer ids sorted.** Each side sees the exchange from
the opposite direction — its own nonce is "mine", the other is "theirs" — so
without a canonical order the two would mix the inputs in opposite orders and
derive different secrets. Sorting by peer id gives both the same view.

The code itself never crosses the network; only `sha256("drop:code:v1:" + code)`
does, as a DHT key. And the pairing handler is removed the moment pairing
finishes, so the surface is not left open.

Both sides then store the secret in `peers.json`, mode 0600. Verified in
testing: after pairing, both files held **byte-identical secrets**.

---

## 6. Rung 3 — NAT, the part that is actually hard

### Why this is a problem at all

Your laptop has `192.168.1.157`. So do a hundred million other laptops. It is
not reachable from outside — your router has one public IP and hands out
private ones behind it. Two machines each behind their own NAT cannot dial
each other, because neither has an address the other can use.

```
   laptop                  NAT A          internet          NAT B         desktop
  192.168.1.157  ────────►  │                                │  ◄──────  10.0.0.4
                            │                                │
                     137.224.252.107                  81.4.109.22
                            │                                │
                            └──── dial? ──►  ✗  ◄── dial? ───┘
                              neither side can start it
```

### Three ways out, in the order drop tries them

**a. Port mapping (UPnP / NAT-PMP)** — `libp2p.NATPortMap()` asks the router
politely to forward a port. When the router agrees, the node genuinely has a
public address and everything else is unnecessary.

```
   node ──"please forward 44061"──► router ──► /ip4/137.224.252.107/udp/44061/quic-v1
                                                        now dialable by anyone
```

This is what actually happened in testing, and it is worth being precise about:
the node reported `reachable` because **UPnP gave it a public address**, not
because a relay was involved. A `/ip4/137.224.252.107/…` address appeared in
its DHT record and the second node dialled it directly.

**b. Circuit relay** — when the router will not cooperate, the node reserves a
slot on a third node that *is* publicly reachable, and hands out an address
that routes through it:

```
                        ┌──────────┐
                        │  relay   │  publicly reachable third party
                        └────┬─────┘
             reservation ┌───┴───┐ dial via circuit
                    ┌────▼──┐ ┌──▼─────┐
                    │ laptop│ │desktop │
                    └───────┘ └────────┘

   address becomes:  /ip4/<relay>/…/p2p/<relay-id>/p2p-circuit/p2p/<laptop-id>
                     ╰──── how to reach the relay ────╯╰─ who to ask it for ─╯
```

The relay forwards **encrypted** bytes. It sees traffic exists; it cannot read
it, because the Noise session is end-to-end between the two nodes.

**c. Hole punching (DCUtR)** — relaying is a bandwidth cost on a stranger, so
libp2p immediately tries to escape it. Coordinated over the relay connection,
both sides dial each other simultaneously; each outbound packet punches a hole
in its own NAT that the other's inbound packet fits through.

```
   step 1:  both connected via relay, exchange observed addresses
   step 2:  ──► simultaneous dial ◄──
   step 3:  NAT A now expects traffic from B, and vice versa
   step 4:  direct connection established, relay dropped
```

Success depends on NAT behaviour — symmetric NATs (which randomise the external
port per destination) defeat it, and those connections stay relayed.

> **Honest status:** rungs (a) and the DHT lookup are proven in testing. The
> relay path (b) and hole punching (c) are wired up but **have never actually
> run here**, because UPnP kept succeeding. `DROP_RELAYS` exists to point at a
> relay you control, which is the reliable answer on a hostile network.

---

## 7. The whole thing, end to end

`drop send report.pdf --to laptop`, on a machine that has never met the
laptop's current network:

```
  ┌────────────────────────────────────────────────────────────────────┐
  │ 1. RESOLVE THE NAME                                                │
  │    "laptop" ──► peers.json ──► peer id + shared secret             │
  └────────────────────────────────────────────────────────────────────┘
                              │
  ┌────────────────────────────────────────────────────────────────────┐
  │ 2. FIND IT                                                         │
  │    mDNS, 3s ......................................... nothing      │
  │    DHT: bootstrap, then look under                                 │
  │         HMAC(secret, this hour) and HMAC(secret, last hour)         │
  │         ──► /ip4/137.224.252.107/udp/44061/quic-v1                 │
  └────────────────────────────────────────────────────────────────────┘
                              │
  ┌────────────────────────────────────────────────────────────────────┐
  │ 3. CONNECT                                                         │
  │    QUIC first, TCP if UDP is blocked                               │
  │    Noise handshake: both ends prove their keys                     │
  │    ✗ wrong key ──► connection refused. impersonation is impossible │
  └────────────────────────────────────────────────────────────────────┘
                              │
  ┌────────────────────────────────────────────────────────────────────┐
  │ 4. TALK                                                            │
  │    /drop/session/1.0.0 ─┬─ files    (known or unknown size)        │
  │                         ├─ messages (chat, links)                  │
  │                         └─ duplex   (terminal, pipe)               │
  │    many streams multiplexed over the one connection                │
  └────────────────────────────────────────────────────────────────────┘
```

Step 4 is worth dwelling on: because QUIC multiplexes, a file transfer, a chat
message and a terminal stream all run **concurrently over one connection**,
each independently flow-controlled. That is why "everything is a conversation"
is cheap rather than expensive.

---

## 8. So how is this like iroh?

You asked, and the honest answer is: **they are the same architecture with
different names.** Both start from "address = public key" and both must solve
the same resolution problem.

| the job | iroh | drop (libp2p) |
| --- | --- | --- |
| permanent address | `NodeId` — ed25519 pubkey | `PeerID` — ed25519 pubkey |
| encrypted transport | QUIC | QUIC (TCP fallback) |
| same-LAN shortcut | local discovery | mDNS |
| "where is this key now?" | pkarr records / DNS | Kademlia DHT |
| NAT traversal | relay-coordinated hole punch | DCUtR over a relay |
| when punching fails | relay the bytes | circuit relay v2 |

The real difference is **who runs the infrastructure**.

```
   iroh                              drop
   ────                              ────
   n0 runs relay servers             public IPFS DHT for discovery
   used by default                   public/your own relays
   works out of the box              you choose, nothing is blessed
   self-hostable if you want         DROP_RELAYS / DROP_BOOTSTRAP to override
```

iroh is more pleasant on day one because someone else's servers are already
running. drop has no privileged party — but "no privileged party" is not the
same as "no infrastructure", and it is worth being blunt about that:

> **No P2P system is infrastructure-free.** Something must always exist for
> first contact and for NAT help. iroh has relays; drop has DHT bootstrap nodes
> and relays. What "fully distributed" honestly means is that *any node can play
> those roles* and the defaults are replaceable — not that the roles vanish.

drop also does one thing iroh does not: the DHT rendezvous is keyed on a
per-pair secret rather than on the public node id, so lookups do not double as
a presence beacon. That is a consequence of building on a general-purpose DHT
where anyone can read any key.

---

## 9. What has to be running

The uncomfortable requirement, true of iroh equally:

```
   drop send file --to laptop
                       │
                       └─► only works if something on the laptop is
                           online, announcing, and holding a relay slot
```

That is `drop daemon`, installed as a user service:

```
   install -m 0644 misc/drop.service ~/.config/systemd/user/
   systemctl --user enable --now drop
```

Without it, both sides have to be running drop at the same moment — which is
croc's model, not "pair once and reach forever". The daemon re-announces every
30 minutes because DHT provider records expire; a node that stops repeating
stops being findable.

---

## 10. Failure modes, and what they look like

| symptom | what is actually wrong |
| --- | --- |
| `no peers on this network` | mDNS only; the other device is on a different subnet. Expected off-LAN. |
| `no bootstrap peer answered` | No internet, or bootstrap nodes unreachable. DHT cannot be joined. |
| `cannot find <id> on the network` | Nothing is announcing under the pair key — the far end's daemon is not running. |
| found it, but the connection fails | Addresses are stale, or NAT traversal lost. A relay is the answer. |
| `not paired with this device` | No shared secret, so no private rendezvous exists. `drop pair` once. |

---

## 11. Where this lives in the code

```
src/pkg/node/identity.go     the keypair, and therefore the address
src/pkg/node/host.go         the libp2p host: transports, NAT, DHT wiring
src/pkg/node/relay.go        relay discovery, static relays from $DROP_RELAYS
src/pkg/discovery/mdns.go    rung 1
src/pkg/discovery/rendezvous.go  rung 2: the pair keys and the window logic
src/pkg/proto/pair.go        the exchange the shared secret comes from
src/cmd/locate.go            the ladder itself, in twenty lines
```

`locate.go` is the shortest useful thing to read: it is the whole strategy with
nothing else in the way.
