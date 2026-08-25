# drop

Distributed peer-to-peer file transfer.

Pair two devices once, by key. After that either can reach the other from anywhere — across NATs,
across networks, through address changes — with no account and no server holding your data.

```
drop pair                      # on one device: prints a ticket
drop pair 9363f77d…#qxwo-e62y  # on the other: done, forever
```

## how it addresses things

A node's address is its ed25519 public key, and it never changes. Finding where a device currently
*is* happens in three steps, cheapest first:

1. **mDNS**, if both machines are on the same network
2. **a rendezvous relay**, under an identity only the two paired devices can compute
3. **a relay**, when a NAT will not allow a direct connection, with hole-punching upgrading to a
   direct link when it can

For that to work the receiving device has to be online, which is what the daemon is for.

## privacy

The rendezvous a device announces under is derived from the secret it shares with each paired peer,
not from its public key:

```
key = HMAC-SHA256(shared_secret, "drop:pair:v1:" + hour)
```

Three consequences. Someone holding your endpoint id cannot look up where you are or watch when you
come online. The key rotates hourly, so learning one buys no lasting handle. And because the secret
is per-pair, one paired device cannot observe your availability to another.

The secret is derived during pairing over a stream QUIC has already encrypted and mutually
authenticated, mixing both sides' nonces through HKDF, salted with both endpoint ids and ordered so the
two ends compute the same value.

## status

Discovery, pairing and transfer work.

- ed25519 identity, persisted; the public half is the address
- pairing over a one-time 60-bit code, hashed into its own rendezvous
- private rendezvous per pair, hourly rotation
- AutoRelay, so a node behind a NAT still has a dialable address
- transfer with blake3 verification, resume, and paired-only acceptance
- directories are not supported yet; files only

## build

```
make build        # the static release binary, into ./drop
make run peers    # run it; bare words pass through
make verify       # fmt-check, vet, test, test-web, build
make test         # the go suite
make test-web     # the page rules, if bun or deno is installed
make install      # into $PREFIX/bin
```

At an oslo prompt in this directory `make` is enough; everywhere else it is `oslo make`.

The toolchain comes from the flake — `nix develop`, or `direnv allow` and it is loaded on `cd`.

### size

The binary is 6.4 MB packed, from 18 MB compiled. `-s -w`, `-trimpath` and
`CGO_ENABLED=0` are already applied — `-s -w` strips DWARF and the symbol table, not
`gopclntab`, which Go needs for panics and has no flag to remove. What is left is genuine
code.

It was 32 MB on libp2p. Moving to iroh took it to 15 MB compiled: 682 packages became 307.

`build` packs with upx, and the compression level is free at startup — `-1`, `-5` and `-9` all
unpack in the same 0.09s — so the default is the smallest of them. Only lzma is slow to
unpack, which is why it is not the default.
whichever of these you are looking at.

```
drop ls beta          the command line: what beta shares with you
drop                  a full-screen interface: enter a device, then a path
```

## commands

```
drop ls [device[/path]]        what a device shares with you
drop to <device>/<path> [args] open a path; what is there decides what happens
drop                           all of it, in a full-screen terminal
                               the first device in the list is this one
                               paths nest: enter walks in, esc walks out
                               p shows a pairing code, t takes one
                               a conversation scrolls with the wheel, ↑↓, pgup/pgdn

drop pair [ticket]             link a device to this one; --qr to show a code
drop passwd                    hash a password, to guard a path with

drop serve                     serve what the config declares, and stay reachable
drop ns                        what this node serves, and who to
drop peers                     the devices this one knows
drop chat <device>             talk to one
drop log [device]              a conversation, or all of them
drop cast                      serve a terminal read from stdin as asciicast
drop id                        this node's identity
drop user                      who this machine belongs to
drop grant <path> <who>        let somebody reach a path
drop revoke <path> <who>       stop them
drop grants                    what has been allowed and refused
drop ask <device>/<path>       ask to be let into a path you can see
drop requests                  who has asked to be let in
drop vault                     whether what is kept on this disk is encrypted
```

## namespaces

One identity per device, and named paths under it. An address is a peer and a path:

```
   workstation/inbox
   workstation/inbox/photos
   workstation/stream/of/one/specific/namespace
   ╰────┬────╯╰──────────────┬───────────────╯
     who                   what
```

Each path has a **type**, and the type is what decides what happens when someone opens it. That is
declared in the config, not chosen by a flag at the far end — so there is one verb:

```
drop to laptop/inbox report.pdf     it is a files namespace, so this sends a file
drop to laptop/inbox -              and - is standard input, whose length is unknown
drop to laptop/logs                 it is a stream namespace, so this reads it
drop to laptop/term                 it is a tty namespace, so this watches it
drop to laptop/chat "on my way"     it is a chat namespace, so this is a message
drop to laptop/open https://…       it is a link namespace, so that opens over there
```

Asking a namespace for something it is not is refused with the reason, rather than half-working:

```
$ drop to laptop/chat report.pdf
drop: 12D3KooW… declined: /chat is a chat namespace
```

## who may reach what

Every path carries an `access` rule, and it **inherits down the tree**. A path with no rule
anywhere above it is reachable by nobody — forgetting to write one closes a path rather than
opening it.

A rule declared deeper **replaces** the one it inherited rather than merging with it, so a
declaration says what it means.

```lua
access = { "bob", "carol" }        -- people, by the name you filed them under
access = { "bob@laptop" }          -- one machine of theirs, and no other
access = { "me" }                  -- any machine of your own
access = "paired"                  -- anyone in your address book
access = "trusted"                 -- only the ones you decided to trust
access = "anyone"                  -- anybody who knows the id, paired or not
access = { keys = { "7b97…" } }    -- a machine that never paired, by its endpoint id
visible = "paired"                 -- they see it exists, and must ask
visible = "trusted"                -- only the people you trust see it
visible = { "carol" }              -- only carol sees it
access = { password = "$argon2id$…" }
access = { paired = { "laptop" }, password = "$argon2id$…", require = "all" }
```

A name on its own is a **person**, not a machine: every machine they have signed a badge for gets
in, including ones you have never met. Recognising somebody and letting them in are separate — see
[who you are](#who-you-are).

An endpoint id is a public key, and QUIC proves possession of the private half during the
handshake — so `keys` is a real cryptographic statement, not a hostname you could spoof. What
pairing buys on top is a shared secret, which is what the rendezvous derives its rotating
identity from.

A password is the weak one: the other two bind to a key nobody else holds, and a password
binds to knowledge, which spreads. It earns its place because it is the only one that works
before you know who is coming. `drop passwd` prints the hash to put in the config — the
plaintext never goes in a file, so a leaked config is not a leaked password.

### paired is not the same as trusted

Pairing is recognition: it means a device arrives with a name instead of as a stranger. Trust is a
second, deliberate step, and it is what the narrow rules are written against — otherwise "everybody
I have ever met" and "everybody I trust" are the same set.

```lua
access  = "paired"    -- anybody in the address book
access  = "trusted"   -- only the ones you said yes to
visible = "trusted"   -- put something up to be asked for, by people you trust
```

Nobody is trusted by pairing alone. Your own machines are trusted by construction — they carry your
user key, so they are you. Trust belongs to the **person**, not to one of their laptops: trusting
bob trusts every machine he signs, now and later.

### the interface is users, then machines

The first screen is **users**, not devices. A user is what everything else is written against —
access rules name people, trust belongs to people, and a machine somebody buys next week is already
covered. Enter a user to see their machines, enter a machine to see what it shares.

```
╭─ users ──────────────────────────────────────────────────────────╮
│   ◈ me                                                2 machines │
│   you — every machine your user key has signed                    │
│   access = { "me" }   ·   2 reachable                             │
│   ★ bob                                               2 machines │
│   a person you decided to trust                                   │
│   access = { "bob" }                                              │
│   ◌ anon                                             one machine │
│   machines paired on their own, belonging to nobody               │
╰───────────────────────────────────────────────────────────────────╯
```

**You are a user like any other** — `me` holds this machine and every other one your user key has
signed. A device paired with `--machine` belongs to nobody, and nobody is a user called **anon**, so
the screen stays one kind of thing rather than three.

### managing somebody

`m` on somebody in the users list opens the screen for them — who they are, whether they
are trusted, and every path you have granted or refused them:

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

`t` changes trust, `x` revokes a path they hold, `f` drops the pairing. It is a separate screen from
the list on purpose: the list answers *what can I open*, which you ask constantly, and this answers
*who is this and what have I given them*, which you ask rarely. Putting both in one list made
neither readable.


### seen, but not open

Between shared and secret there is a third thing: a path that says it exists and refuses to be
opened. It appears in a listing marked **locked**, and whoever sees it can ask.

```lua
drop.mount("/vault", { type = "files", dir = "~/vault", visible = "paired" })
drop.mount("/work",  { type = "chat", access = { "bob" }, visible = { "carol" } })
```

`visible` is its own option rather than a rung inside `access`, because it answers a different
question: access says who gets in, visible says who is told there is a door. A path can have both —
shared with one person, merely visible to another, who then asks.

```console
$ drop ls beta
  /vault   files    locked

$ drop ask beta/vault --why "for the thing we discussed"
asked beta for /vault
nothing is granted by asking: somebody there decides.
```

On the other machine the request waits until somebody answers it:

```console
$ drop requests
  /vault
    from  carol
    why   for the thing we discussed

$ drop requests allow /vault carol
```

In the interface it is the same two keys as everything else: `a` on a locked path asks for it, and
`a` on a waiting request grants it. Answering either way takes the request off the list — a list
that kept answered requests is a list nobody reads.

**Asking grants nothing.** The request is a note on somebody's disk. A refusal still beats it, so
revoking somebody also stops them seeing what they used to reach and asking for it again.


### granting and revoking

The config is structure, written by hand. Who has been let in since is data, and drop owns it —
the same split as `sshd_config` and `authorized_keys`, so that a program editing one never mangles
the other.

```console
$ drop grant  /work carol@laptop     # on top of whatever the config says
$ drop revoke /work bob@phone        # against it
$ drop revoke /work bob@phone --forget
$ drop grants
```

The interface does the same thing: on one of your own paths, `w` opens **who may reach it** --
people, machines, and the rules that name nobody -- and `a` and `x` allow and refuse from there.

A refusal beats every rule there is, including one you wrote, and takes effect on the **next
connection** rather than when a badge expires. It is local: this machine stops trusting them, and
nobody else is told. Grants live in `$XDG_CONFIG_HOME/drop/grants.json` and cover everything under
the path they are written at, so refusing somebody at `/` refuses them everywhere.

### listings are filtered, not refused

`drop ls beta` shows what beta shares **with you**. A path shared with someone else is absent,
not marked refused: a listing that showed the whole tree would tell someone which machine has
a terminal worth attacking.

A path guarded by a password is in no listing at all — nobody offers a secret to ask what
exists — so whoever you hand one to needs the path as well as the word.

### the types

| type | what opening it does | needs |
| --- | --- | --- |
| `files` | receives files into a directory | `dir` |
| `stream` | runs a command and sends what it writes, for as long as it writes | `command` |
| `tty` | a shell in a pty; read-only unless told otherwise | `shell`, `input` |
| `chat` | conversation messages | |
| `link` | a URL, optionally handed to a browser | `action` |

### the longest prefix wins

A mount serves everything below it, so `/stream` answers
`/stream/of/one/specific/namespace` without declaring each one — and a more specific mount still
takes precedence:

```
   declared:   /stream            /stream/logs
   asked:      /stream/a/b        →  /stream        rest /a/b
               /stream/logs/today →  /stream/logs   rest /today
               /streaming         →  nothing        (boundary, not substring)
```

## who you are

A device has an identity of its own — the endpoint key, which QUIC proves on every connection. That
says *which machine*, never *whose*. A **user key** says whose.

```
  you ──── one SSH key ────┬──── laptop     each machine carries a badge:
                           ├──── yubikey    "this user owns this machine,
                           └──── server      called <name>, until <date>"
```

An ed25519 SSH key, generated on first run if you do not point drop at one, and read through
`ssh-agent` if you do — so a key on a YubiKey, in a PIV slot, or in a file all work the same way.
`drop user` shows what this machine is using.

```console
$ drop user
  key      ~/.config/drop/user
  identity ssh-ed25519 AAAAC3Nza…
  as       SHA256:nkTYln1x9SMCjQxJCxVC9ng4829/4DxcU6CK+iKNHaw

  this machine is "tron", until 2026-11-22
```

Each machine holds a **badge**: a statement signed by your user key, in OpenSSH's own signature
format under drop's namespace, so it can be checked with `ssh-keygen -Y verify` and cannot be
replayed as a git signature or an ssh login. It is signed once, at enrolment, and shown on every
connection after that — a touch per connection would be unusable, and a touch every ninety days is
not.

**Pairing is with a person.** The exchange carries the badge, and both sides write down the other's
user key. A machine of theirs you have never met then presents its own badge and is recognised
without pairing again: *pair once per person, not once per pair of machines*.

```console
drop pair <ticket>             the user key is learnt; their other machines work later
drop pair <ticket> --machine   this device and no other
```

`--machine` is for a build server, a box that is nobody's personal identity, or a deliberate
refusal of transitive trust.

**Recognising somebody is not letting them in.** Pairing with bob grants his machines nothing at
all — it means his phone arrives as `bob@phone` rather than as a stranger. What it may then reach
is whatever the rules on each path say, and the default is nothing.

### one key, every machine

The user key is per **person**, not per machine. Put the same one on each machine you own and they
all answer to `"me"`; let each generate its own and they are strangers to each other.

```lua
drop.user_key = "~/.ssh/id_ed25519"         -- a key you already have
drop.user_key = "~/.ssh/id_ed25519_sk.pub"  -- a YubiKey, signed through ssh-agent
```

The public half is enough when the private one is in hardware: drop cannot talk to a security key —
that is CTAP, PIV or a vendor's protocol, none of which belongs in here — so signing a badge is a
**command**, which reads what to sign on standard input and writes the signature on standard output.

```lua
drop.user_sign = "ssh-keygen -Y sign -f ~/.ssh/id_yubi -n drop"   -- the default, spelt out
drop.user_sign = "my-signer --whatever"                           -- anything that can reach the key
```

Unset, drop works it out: a key it can read is signed in process, with no command and no touch; a
key it cannot is signed by `ssh-keygen -Y sign`, which every machine with SSH already has and which
drives a security key directly — **no ssh-agent involved**. A key drop was *pointed at* and cannot
find is an error: it will not answer a typo by inventing a second identity. Without `user_key` at
all, drop keeps a key of its own.

### another person, same machine

`$DROP_PROFILE` is a whole other identity: its own device key, user key, address book and
conversations, under `…/drop/profiles/<name>/`, on a port derived from the name so two can run at
once. Two profiles are strangers who must pair, which is how a rule that names somebody else gets
tried without a second computer.

```console
$ DROP_PROFILE=bob drop user      # a different person, called tron-bob
$ DROP_PROFILE=bob drop serve     # alongside your own, on its own port
$ drop pair                       # then pair them, as you would two machines
$ DROP_PROFILE=bob drop pair <ticket>
```

A profile that sets `drop.user_key` to the same key you use is *you* again — leave it out of a
profile's config for a genuinely separate person.


**Revocation** is expiry and a local refusal, and nothing more honest is possible without a server.
A badge lasts ninety days, so a lost machine stops being trusted within ninety days rather than
today; `drop peers rm bob@laptop` stops this machine trusting it immediately, and tells nobody
else.

## configuration

`$XDG_CONFIG_HOME/drop/init.lua`, or `$DROP_CONFIG`. Settings are assigned, namespaces are
registered, and the file returns nothing — so it is a program, and a machine can decide for itself
what it offers:

```lua
local drop = require("drop")

drop.name = "workstation"
drop.open_links = true
drop.rendezvous = false -- optional; on unless you say otherwise, see below

-- A branch: no type, just who may reach everything under it. Access inherits downward, and
-- a path with no rule above it is reachable by nobody.
drop.mount("/work",    { access = { "laptop", "phone" } })
drop.mount("/friends", { access = { "bob", "carol" } })

drop.mount("/work/inbox", { type = "files",  dir = "~/Downloads" })
drop.mount("/work/logs",  { type = "stream", command = "journalctl -f -n 50" })
drop.mount("/work/term",  { type = "tty",    shell = "/bin/sh", input = false })

drop.mount("/friends/chat", { type = "chat" })
drop.mount("/friends/open", { type = "link", action = "xdg-open" })

-- A deeper rule replaces the one above it rather than adding to it.
drop.mount("/friends/just-bob", { type = "files", dir = "~/bob", access = { "bob" } })

if os.getenv("DROP_DEV") then
  drop.mount("/work/build", { type = "stream", command = "tail -f /tmp/build.log" })
end
```

### Finding a device that moved

On one network, devices find each other by multicast and need nothing else. A device that
moves — a laptop that left the building — cannot be found that way, because the address its
peers wrote down at pairing no longer reaches it.

So drop publishes its current address for paired devices to find. **This is on by default**: a
laptop that changed networks is the ordinary case, and a program that only works while both
machines are on one wire is not worth pairing with. `drop.rendezvous = false` turns it off for
somebody who would rather a device be unreachable than announced at all.

What gets published is not your identity. For each device you have paired with, drop derives
a throwaway identity from the secret the two of you established when you paired:

```
identity = ed25519(HKDF(pair secret, publisher, hour))
```

Both sides can compute it and nobody else can, because it takes the pair secret. So:

  - Someone who knows your device ID still cannot locate you. The ID is not what the record
    is filed under.
  - A device paired with three others publishes three unrelated records. Nothing ties them
    to each other or to you.
  - The identity rotates hourly, so a relay cannot watch one record over weeks.
  - Only relay addresses are published, never your IP.

The cost is that connections may cross a relay, and the relay knows two parties are talking
even though it cannot tell who they are or read anything. Traffic stays end-to-end encrypted.
Set `drop.relays` to your own if you would rather not use the defaults.

Mounts are keyed by path, so declaring one twice replaces it rather than adding a second — a config
that loops, or is re-read, cannot silently grow the table.

A file that exists and does not parse is **fatal**, and the error names the file and line. A typo
that silently drops half the namespaces is worse than not starting. With no file at all, drop serves
a small default: `/inbox`, `/chat`, `/open`, and nothing that runs a command or shares a terminal,
because those are decisions.

`drop ns` prints what this node serves. `misc/init.lua` is a worked example.

## conversations

Everything drop does is a **conversation with an endpoint id**. Files, chat, links, terminals and
endless streams are modalities inside it, not separate features that happen to share a binary:

```
drop chat laptop                        # talk
drop log laptop                         # the whole story, in one place
```

and the log reads as one thing, because it is:

```
11:59 ← beta         first message, sent while you were asleep
12:01 ← beta         link  https://example.com/thing-i-was-reading
12:01 ← beta         file  report.pdf (390.6 KB)
12:01 ← beta         that is the report you asked for
```

### saying something to a device that is off

A message is recorded the moment it is composed. What is uncertain is whether it *arrived*, not
whether it was said, so `drop say` never fails because the far end is asleep — it queues, and goes
out when the device appears. `drop chat` retries in the background while you keep typing.

Delivery is acknowledged **per message, after it is on the far end's disk**. Acknowledging before
storing turns a crash into silent loss: the sender drops it from its outbox and then nobody has it.
Anything unacknowledged stays queued, so a partial delivery is retried rather than lost.

### how it is stored

Two files per peer under `$XDG_DATA_HOME/drop/convo/<peer-id>/`:

| | |
| --- | --- |
| `history` | append-only, never rewritten. A crash can truncate the tail; it cannot corrupt what came before, and reading stops at the tear rather than throwing away the rest. |
| `outbox` | only what is undelivered. Small enough to rewrite whole, and replaced by rename so an interruption leaves either the old queue or the new one. |

Both use the same binary codec as the wire, so what is stored is what was sent.

Message ids are ULID-shaped — 48 bits of milliseconds then 80 of tail — and **strictly increasing**,
not merely time-ordered. Two lines typed in the same millisecond would otherwise sort by a random
tail and appear in the wrong order; a clock that steps backwards is handled the same way, because an
id smaller than the last one has the same effect. Ids are also how a resend is told from a first
delivery, so a reconnect does not duplicate the backlog.

## what is kept on disk

Conversations are written to `$XDG_DATA_HOME/drop/convo/<peer id>/`. Without a vault they are
written in the clear, and `0600` under `0700` stops another account on the same machine and nothing
else:

```console
$ strings ~/.local/share/drop/convo/d04c…/history
hey, this is from the terminal
```

A **vault** changes that. One data key — thirty-two random bytes — encrypts every record; the data
key itself is written once, encrypted to whoever you name, and unwrapped once at startup. A touch
per message would be unusable; a touch per start is not.

```lua
drop.vault = "~/.config/drop/vault.key"            -- protects a backup, not a live disk
drop.vault = { "age1yubikey1…", "age1…backup…" }   -- unreadable without the key
```

Always name more than one when you name hardware: a data key wrapped only to a YubiKey is a history
that dies with the YubiKey.

```console
drop vault           what it is doing, creating nothing
drop vault seal      encrypt what is already on this disk
drop vault clear     put it back in the clear
```

Both walks read every record and write it back — sealed or not — so turning a vault on does not
hide what came before it, and turning one off is not a loss. Stop drop first: a message that lands
during the walk is in neither file afterwards.

**What it protects:** a machine that is off. A stolen laptop, a pulled disk, a leaked backup. That
is the ordinary way private things escape.

**What it does not:** a machine that is running. drop has to read the data to show it to you, so
anything with your session can ask drop. No design at this level changes that.

**What still leaks** even sealed: directory names are peer ids, and their sizes are visible — who
you talk to, and roughly how much. Files that arrived are left alone; somebody asked for them, in a
directory they chose. `peers.json` is in the clear, and whoever takes it can *be* you to every
device you have paired with.

A locked device — the key unplugged, the file gone — is a locked device, not an empty one. drop
says so rather than reporting a conversation that is there as missing.


## the wire

Binary, varint-based. No JSON, no reflection, no codegen.

Everything drop moves runs over one protocol, `/drop/session/1.0.0`, as a sequence of frames:

```
frame = [1 byte kind][uvarint length][body]
```

A body is either raw bytes (data frames) or a packed message: varints, length-prefixed strings, no
field names on the wire. A `uint` under 128 costs one byte; sizes are zigzag so `-1` costs one too.

Every read on a stream goes through one buffered reader. That is what makes it safe to mix control
frames with bulk data: nothing else reads from the stream, so nothing can consume bytes belonging
to the next frame.

### sizes, known and unknown

An item carries `Size`, and `-1` means nobody knows yet:

| | known size | unknown size |
|---|---|---|
| where from | a file on disk | stdin, a pipe, a terminal |
| how it ends | the count runs out | an `End` frame |
| resume | yes, from bytes already held | no — a partial is indistinguishable from a whole one |
| progress | a percentage | a running total |
| verification | blake3, both ways | blake3, both ways |

Both end the same way: an `End` frame carrying the length the sender actually wrote and a blake3
digest of all of it. For an unknown-size item that frame is where the length is finally learned.
The receiver checks both before renaming anything into place.

### the two modes

**Files** is one side pushing items:

```
sender                          receiver
  Open{files, sizes}   ------->
        <------- Accept{resume[]}      // per item, bytes already held
  Data ... Data        ------->
  End{size, digest}    ------->
        <------- Ack{ok}               // hashed and verified
```

**Duplex** is a live stream, both ends writing at once, nobody counting:

```
  Open{duplex}         ------->
        <------- Accept
  Data          <-- both ways -->  Data
  Resize        <-- both ways -->  Resize
  End (half-close)     ------->
```

The two directions are independent. One end finishing does not end the other — the same way a pipe
closing its input does not stop it producing output. `Close` writes an `End` and half-closes the
stream, so the far end reads a real end of file while its own writes keep working.

## sharing a terminal

Two different things share a terminal, and they are not the same one.

A **tty namespace** hands the far end a shell on this machine. It is declared in the config, it
starts when somebody opens it, and `input` decides whether they may type into it:

```
drop.mount("/term", { type = "tty", access = { "laptop" }, shell = "/bin/sh", input = true })
```

```
drop to laptop/term            open it; what you type goes there if it takes input
```

A **cast** is the terminal you are already sitting at, shown to whoever is watching. It reads
asciicast v2 on standard input, so anything that writes asciicast will do:

```
asciinema rec --stdout | drop cast
HEXE_SHARE_BACKEND="drop cast" hexe ...

drop to laptop/cast            watch it
```

A cast is served by the node that is already running, if one is: it hands the recording to
`drop serve` over a local socket rather than standing up a second endpoint. Two listeners cannot
share one identity, and a watcher dialling the address in its address book has to reach the one
that knows about the cast. With no daemon running, the cast serves itself.

`/cast` exists only while somebody is casting. Before and after, it is absent rather than a path
that answers with nothing behind it.

Watchers are read-only: a cast is somebody's screen, and typing into it is a different grant —
that is what a tty namespace with `input` is for.

Someone attaching mid-session gets the last 128KB of output replayed, because a terminal is a
stream of escape sequences and a watcher starting from nothing sees a blank screen until the
next full redraw.

The casting terminal's size is authoritative and is sent on connect and on every SIGWINCH. A
watcher with a smaller terminal is told its output will wrap rather than silently getting a
mangled render.

A watcher that cannot keep up is dropped rather than fed a stream with holes in it: a terminal
rendered from a gappy byte stream is worse than one that stopped.

### when only one side can be reached

Behind a strict NAT a device can open connections and nothing can open one to it. Its address never
resolves to anywhere anybody can dial, so a queue for it would wait forever while the device itself
sits there connected and idle.

So the direction of a connection is not the direction of the traffic. A device holds a session open
to everybody it has paired with, whether or not it has anything to say, and the far end keeps that
connection and pushes whatever is waiting back down it. QUIC does not care which side opened a
connection; either can start a stream on it.

```
    behind a NAT                              reachable
    ────────────                              ─────────
    holds a connection open  ──────────────▶  keeps it
                             ◀──────────────  pushes what is queued
```

Nothing here depends on the NAT being friendly, on a relay hole being punched, or on the unreachable
side being findable at all. It only needs it to be able to dial out, which is the one thing such a
device can always do.

### watching, and typing

A terminal takes its shape from whoever is looking at it, whether or not they may type into it: a
pty drawing for a window nobody has wraps every line in the wrong place. Shape is presentation, not
input, and it is sent either way.

What is typed is the part `input` decides, and the far end is what decides it — anything sent to a
read-only terminal is dropped there rather than being refused here.

In the interface, `i` gives a terminal the keyboard and **ctrl+]** takes it back. While it has the
keyboard it gets every key there is, `esc` and `q` included, because half a keyboard is not a
terminal. The panel says `· typing` so you can see where your keys are going.


## staying reachable

`drop serve` keeps the node reachable. Install it as a user
service so the device is reachable whenever it is on:

```
install -m 0644 misc/drop.service ~/.config/systemd/user/
systemctl --user enable --now drop
```

Without a daemon, `--to laptop` only connects while someone is running `drop recv` on the laptop.

## layout

```
src/main.go            entry point; the version lives here
src/cmd/               the cobra command tree, one file per command
src/pkg/node/          identity, the iroh endpoint, relays
src/pkg/discovery/     finding a device on this wire
src/pkg/rendezvous/    finding one that moved, under a derived identity
src/pkg/ns/            paths, kinds, and the access rules on them
src/pkg/passwd/        argon2id, for the secrets that guard a path
src/pkg/proto/         pairing, hello, transfer, and the framing under them
src/pkg/book/          the address book, including pairing secrets
src/pkg/grant/         who has been let in and shut out from the interface
src/pkg/asked/         requests to reach a path, waiting on an answer
src/pkg/vault/         the key everything on this disk is encrypted with
src/pkg/seen/          devices that dialled and were turned away
src/pkg/shares/        what each device last said it shares
src/pkg/user/          a person, and the badge each of their machines carries
src/pkg/convo/         the durable conversation log and outbox
src/pkg/term/          a terminal screen, rebuilt from what a device sends
src/pkg/cast/          one terminal fanned out to many watchers
src/pkg/asciicast/     reading an asciicast stream
src/pkg/ticket/        a pairing invitation, as text, link, or QR
src/pkg/tui/           the full-screen interface
src/pkg/conf/          the Lua configuration
misc/                  the systemd user unit
```

## one node, however many commands

`drop serve` holds a connection to every device you have paired with, and every command borrows
them over a socket on this machine rather than standing up a node of its own.

The difference is the whole cost of reaching somebody: a rendezvous lookup, a relay session and a
handshake — seconds — against a stream on a connection that already exists.

```
drop ls laptop        55ms with a daemon running, seconds without
```

The same socket carries `drop cast` and the pairing offer, for the same reason: two processes
cannot share one address, so the one holding it does the work.

With nothing running, every command dials for itself, exactly as before.

## testing it

`make test` is the unit suite. `make e2e` is two real nodes on this machine, driven from the
command line over QUIC: pairing, a message each way, a file each way, standard input as a file, a
link, a stream, a shell, a cast, and a message queued for a device that was switched off.

It is behind a build tag and not part of `make test`, because it builds the binary, starts daemons
and takes a minute.

```
make e2e
```

## environment

```
DROP_NAME          what this node calls itself; defaults to the hostname
DROP_PORT          the port to listen on; defaults to 47777
DROP_RELAYS        relay urls to use instead of the defaults, when a rendezvous is on
DROP_CONFIG        the config file; defaults to $XDG_CONFIG_HOME/drop/init.lua
DROP_USER_KEY      the user key to sign badges with; overrides drop.user_key
DROP_PROFILE       run as another person on this machine, with its own keys and port
DROP_NO_MDNS       turn the local wire off, so finding a device has to go out and back
DROP_NO_PUBLISH    publish where this device is nowhere, so nothing can look it up
DROP_OPENER        what opens an arriving link; defaults to xdg-open
XDG_CONFIG_HOME    where identity, peers.json and grants.json live; defaults to ~/.config
XDG_DATA_HOME      where conversations live; defaults to ~/.local/share
```

`peers.json` holds pairing secrets and is written 0600.
