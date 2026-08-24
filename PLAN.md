# Where drop is going

What works today is in the README. This is what does not exist yet, why it should, and what has
already been decided about how.

Two things run through all of it:

- **Who you are is not which machine you are sitting at.** That is the next structural change, and
  it is not optional — every access rule worth writing needs it.
- **What is kept on disk is your business.** Encryption at rest is worth having and is a choice,
  not a default, because the honest version of it costs something on every machine that has no
  hands near it.

---

## 1. People, and the machines they own

Today there is one kind of identity: a keypair per machine. Pairing links two *machines*, and an
access rule names *machines*. This gets worse with every machine added — two people with three
machines each is nine pairings, and "bob may read /work" has to be written as three device names
and rewritten when bob buys a laptop.

```lua
drop.mount("/work",    { access = { "bob" } })          -- any machine bob owns
drop.mount("/keys",    { access = { "bob@yubikey" } })  -- that one machine
drop.mount("/scratch", { access = { "me" } })           -- any machine I own
```

### The design, and where it comes from

[Matrix cross-signing](https://github.com/matrix-org/matrix-doc/blob/master/proposals/1756-cross-signing.md)
solved this without asking anybody to trust a server: a person has a master key, it signs their own
devices, and verifying the person once means every device they sign is trusted from then on. drop
needs that shape, minus the parts that exist so a homeserver can publish trust — drop's address
book *is* that record.

[`did:key`](https://w3c-ccg.github.io/did-key-spec/) was considered and does not fit. It is a fine
way to *spell* a person's identity — self-certifying, no registry, no network — but it is derived
from a single key and cannot enumerate that person's machines, which is the entire problem.

**A user key.** One ed25519 SSH key per person. Not a new kind of key nobody has: an SSH key is a
signing key by design, most people already have one, and it covers every level of care with one
mechanism.

| where it lives | what it is | how it signs |
| --- | --- | --- |
| a YubiKey | `ssh-keygen -t ed25519-sk -O resident` | never leaves the hardware, a touch per signature |
| PIV slot | reached through `libykcs11.so` | never leaves the hardware |
| a file | `~/.ssh/id_ed25519`, or one drop makes | read and signed directly |

drop asks `ssh-agent` to sign and does not care which of these is behind it.

**A badge.** Each machine keeps a statement signed by the user key:

```
this user <user pub> owns this machine <device pub>, called "laptop", until <date>
```

signed and checked with OpenSSH's own signature format under drop's own namespace:

```
ssh-keygen -Y sign   -f user -n drop badge
ssh-keygen -Y verify -f allowed -I bob -n drop -s badge.sig < badge
```

The namespace matters: a drop badge cannot be replayed as a git commit signature or an ssh login,
and neither of those can be replayed as a badge. **This has been run on real hardware** — a
YubiKey-resident `ed25519-sk` key signing with a touch, verified with nothing but the public half,
no touch and no key present.

The transport already proves the *machine*: iroh authenticates the device key on every connection.
The badge is what turns "some machine" into "a machine of bob's".

**Signing is rare on purpose.** A touch per connection would be unusable. A badge is signed once, at
enrolment, and presented on every connection until it expires.

**Pairing becomes person to person.** The ticket carries the user key; the address book holds a
person and the machines of theirs that have been seen. A machine that has never been met presents a
badge and is recognised without pairing again. *Pair once per person, not once per pair of machines.*

### Recognising somebody is not letting them in

These are two things, and today drop smears them together: `access = "paired"` means having paired
is having permission. They come apart here, and the distinction is what makes person-level pairing
safe.

```
pairing  ──▶  who I recognise       (identification)
access   ──▶  what they may reach   (authorisation)
```

Pairing with bob grants bob's machines **nothing**. It means his phone arrives as `bob@phone`
instead of as a stranger. What it may then reach is decided by the rules on each path, and the
default is nothing:

```lua
drop.mount("/work", { access = { "bob" } })         -- any machine of bob's
drop.mount("/keys", { access = { "bob@laptop" } })  -- his laptop, and nothing else
```

Write only `bob@laptop` rules and his phone can connect, be identified, and reach nothing at all.

**Pairing with a machine rather than a person** is still worth having — a build server, a box that
is not somebody's personal identity, or a deliberate refusal of transitive trust:

```
drop pair <ticket>             person-level: the user key is learnt, and machines
                               signed by it work later without pairing again

drop pair <ticket> --machine   machine-level: this device key and no other. The rest of
                               that person's machines stay strangers, badge or no badge
```

### Mandatory, and invisible

Identity is not optional — it is what access rules are written against. But mandatory must not mean
mandatory hardware: a server with nobody near it has to come back after a reboot at four in the
morning. So drop generates an ed25519 key on first run if it is not pointed at one, and a YubiKey is
an upgrade somebody chooses, not a thing they must own.

### Revocation, which has no clean answer without a server

- **Expiry.** Badges last ninety days and are re-signed by a machine holding the user key. A lost
  machine stops being trusted within ninety days, not today.
- **A local refusal.** `drop peers rm bob@laptop` stops *this* machine trusting it immediately, and
  says nothing to anybody else.
- **Gossip.** Devices pass revocations on when they meet. Real, and a project of its own.

The first two are worth building. The third is worth writing down and not building yet. What must
not happen is pretending revocation works when it does not.

### Getting there without breaking what works

1. The user key and badges, generated and ignored by everybody.
2. Hello and pairing carry them. `ns.Caller` gains `User` and `Device`.
3. Rules learn people: `"bob"`, `"bob@laptop"`, `"me"`. Machine names keep working.
4. The address book learns people. Existing entries become a person with one machine and no user
   key — a legacy pairing that still works on the device key alone. **Nothing needs re-pairing.**
5. The interface groups by person: your machines under `ME`, everyone else under their name.

---

## 2. Saying who may reach what, from the interface

### The ladder, widest to narrowest

| rule | admits | today |
| --- | --- | --- |
| `public` | anyone who knows this device's id | **new** |
| `password` | whoever presents the secret | exists |
| `paired` | any device in the address book | exists |
| `bob` | a person — any machine of theirs | **new** |
| `bob@laptop` | one machine of a person | **new** |
| `keys = { "7b97…" }` | a bare id that never paired | exists |

`public` is the only genuinely open one and deserves saying plainly: anybody who learns this
device's id can reach that path. That is a public file server or a mailbox anyone may post to,
which is a reasonable thing to want and an unreasonable default. Per path, opt in, never inherited
from anything.

### Both lists group, because they hold different kinds of thing

```
╭─ devices ──────────────────────────────╮   ╭─ /work · who may reach it ─────────────╮
│ ╌╌╌╌╌╌╌╌╌╌╌╌ ME ╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌ │   │ ╌╌╌╌╌╌╌╌╌╌╌ PEOPLE ╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌ │
│  ◈ bresilla                        you │   │  ✓ bob              any machine of his │
│      tron      ● this device           │   │    dave             not allowed        │
│      laptop    ● reachable             │   │ ╌╌╌╌╌╌╌╌╌╌ MACHINES ╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌ │
│ ╌╌╌╌╌╌╌╌╌╌╌ PEOPLE ╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌ │   │  ✓ carol@laptop     that machine only  │
│  ◈ bob                                 │   │  ✗ bob@phone        revoked 2 Aug      │
│      laptop    ● reachable             │   │  ✓ 7b97…            never paired       │
│      phone     ○ badge expired         │   │ ╌╌╌╌╌╌╌╌╌╌╌ ANYONE ╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌ │
│ ╌╌╌╌╌╌╌╌╌╌ MACHINES ╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌ │   │    ○ public         anyone with the id │
│  ◈ buildbox    ● no user               │   │    ○ password       whoever has it     │
│ ╌╌╌╌╌╌╌╌╌╌╌╌ SEEN ╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌ │   ╰────────────────────────────────────────╯
│  ◈ 4f2a…       dialled, refused        │
╰────────────────────────────────────────╯
```

**MACHINES** is where a device with no person goes: paired with `--machine`, or paired before any
of this existed. Every pairing that exists today lands here and keeps working.

**SEEN** is devices that dialled and were refused. Without it, allowing a bare id means copying
sixty-four characters of hex out of a log by hand. It dialled — drop already knows it.

### The interface must not write your config

Editing a hand-written config from a program is how configs get mangled. So a grant is *data* drop
owns, merged with the rule that was written by hand:

```
init.lua      access = { "bob" }        structure, yours, hand-written
grants.json   allow: [ "carol@laptop" ] data, drop's, edited from the interface
              deny:  [ "bob@phone" ]

deny beats allow, always
```

The same split as `sshd_config` and `authorized_keys`. It is also what gives revocation an
immediate form: revoking from the interface writes a deny that takes effect on the next connection,
rather than waiting ninety days for a badge to expire.

---

## 3. Encrypting what is kept — the user's choice

Conversations are written to `$XDG_DATA_HOME/drop/convo/<peer id>/history` **in the clear**. `0600`
under `0700` stops another account on the same machine and nothing else:

```
$ strings ~/.local/share/drop/convo/d04c…/history
hey, this is from the terminal
```

Access rules do not help here. They decide what another *device* may reach over the wire. This is
about a disk that is no longer in your hands.

**What it can do:** protect a machine that is off — a stolen laptop, a pulled disk, a leaked backup.
That is the ordinary way private things escape and it is worth fixing.

**What it cannot do:** protect a machine that is running. drop has to read the data to show it to
you, so anything with your session can ask drop. No filesystem-level design changes that.

### The shape

Envelope encryption, which is what age does internally, and where an age key finally has a job here.

**A data key** — thirty-two random bytes, made once, encrypting everything on disk.

**Wrapped to recipients** — stored as `keys/data.age`, encrypted *to* age recipients. Always more
than one: a data key wrapped only to a YubiKey is a history that dies with the YubiKey.

**Unwrapped once** — at startup, one touch, held in memory until the daemon stops. Not per message.

**Records encrypted in place** — the store is already append-only, one length-prefixed record at a
time. Each payload becomes XChaCha20-Poly1305 with its own nonce and the peer id and record id as
associated data, so a record cannot be moved into another conversation or replayed. Appends, reads
and the outbox keep working.

A future note type gets all of this for nothing.

### Off by default, and honest about the difference

```lua
drop.vault = { "age1yubikey1…", "age1…backup…" }   -- unreadable without the key
drop.vault = "~/.config/drop/vault.key"            -- protects backups, not a live disk
```

A key file next to the data it unlocks stops a resold disk and a leaked backup, and stops nothing at
all on a machine somebody already has. Worth having, worth saying plainly.

### What still leaks

**Who you talk to.** Directory names are peer ids and their sizes are visible. Fixable by naming
them `HMAC(data key, peer id)`, at the cost of `drop log` looking empty on a machine whose key is
not plugged in.

**Files that arrived** are left alone. Somebody asked for them, in a directory they chose.

### `peers.json` is a harder question

It holds the secrets pairing established. Whoever takes it can *be* you to every device you have
paired with — a worse loss than the messages beside it. Encrypting it to a YubiKey is the strong
answer and means **drop cannot start without the key plugged in**: right for a laptop, wrong for a
server. Offered, defaulted off, described exactly like this.

### Order of work

1. The data key, wrapped and unwrapped, doing nothing yet.
2. Records encrypted on write, both forms readable on read.
3. A command to encrypt what is already there, and one to undo it.
4. Directory names, once the rest is proven.
5. `peers.json`, opt in, with the startup cost said out loud.

---

### A peer never sees your key

Encryption at rest is local to a disk. It has nothing to do with what a paired device reads:

```
   YOUR DISK                    THE WIRE                    THEIR DISK
   ─────────                    ────────                    ──────────
   ▓▓▓ encrypted ──┐                                    ┌── whatever their
   with YOUR key   │                                    │   config says
                   ▼                                    │
              your daemon                               │
           (key unwrapped,                              │
            held in memory)                             │
                   │                                    │
              plaintext ──▶ QUIC, encrypted ──────▶ plaintext
                            device keys, always on
```

Two layers that never meet. A peer never sees the data key, never needs it, and cannot tell whether
the disk it is reading from is encrypted.

Which leaves one thing to decide: **a locked vault is a locked device.** With the key unplugged the
daemon cannot read what a peer asks for, and the peer has to be told *that device is locked* rather
than handed an empty answer that looks like the path is gone.

## 4. Smaller things that are missing

- **A device that is off cannot be opened.** The path list comes from the device, so with nothing
  cached there is no way into a conversation that is sitting on this disk. Reading and queueing to
  an absent device is the whole point of a queue.
- **Presence.** The daemon holds connections to every paired device and therefore knows who is
  reachable. The list does not show it.
- **Your own files namespace** offers to send a file rather than listing what is in the directory.
- **What a device shares is forgotten on exit.** The cache lives as long as the interface does.
- **Sending a file from Android's share sheet** was never wired up.

---

## 5. Interfaces, later

The web and phone interfaces were scrapped: a laptop scanned a pairing code and timed out, and
nobody could say whether the fault was the interface, the transport or the pairing, because all
three were new at once. The terminal comes first, and the protocol gets exercised from a command
line where there is one place a fault can be.

What was learnt is worth keeping:

- **A dark palette taken from [Radix Colors](https://www.radix-ui.com/colors/docs/palette-composition/understanding-the-scale)**,
  where every step has a documented job, rather than a dozen hex values that only work against each
  other.
- **Camera scanning that worked**, with the decoding in Go because `BarcodeDetector` does not exist
  outside Android and ChromeOS.
- **Camera on Android with no Java at all**, through NDK Camera2 and cgo. It compiled and linked; it
  was never seen to open a camera.

The question to settle before starting again: **Gio, or `gomobile bind` under a Kotlin interface?**
Gio bought one interface for two places and made every platform feature — a camera, a share sheet,
an icon — a piece of systems work. `gomobile bind` compiles the Go into a library, generates Java
bindings, and leaves the interface to Compose: a second interface to write, and every platform
feature handed back. If Android matters more than the browser, that is the road with answers on it.

---

## 6. Still to decide

1. **Where the user key lives** by default — a file drop generates, with a YubiKey as an upgrade.
   Believed settled; say so and stop asking.
2. **Revocation** — expiry plus local refusal now, gossip written down and not built.
3. **Gio or Kotlin** for whatever interface comes after the terminal one.
