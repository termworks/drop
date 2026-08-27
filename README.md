# drop

Distributed peer-to-peer file transfer.

Pair two devices once, by key. After that either can reach the other from anywhere — across NATs,
across networks, through address changes — with no account and no server holding your data.

```
drop peer pair                      # on one machine: prints a ticket
drop peer pair 9363f77d…#qxwo-e62y  # on the other: done, forever
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

- ed25519 identity, taken from the hardware where the machine will say what it is, so a reinstall
  comes back as the same machine and a backup carried elsewhere does not
- one machine, several accounts: each reachable as itself, tied together by a machine-signed plate
- moving to new hardware without pairing again, by a handover the old machine signs
- pairing over a one-time 60-bit code, hashed into its own rendezvous
- private rendezvous per pair, hourly rotation
- AutoRelay, so a node behind a NAT still has a dialable address
- transfer with blake3 verification, resume, and paired-only acceptance
- a push carries files, not whole directories, yet

## build

The build is [`.make.lua`](.make.lua), as recipes. `make` on its own lists them with what each one
says it does.

```
make build        # the release binary, into ./drop
make run peers    # run it; bare words pass through, flags go in --args
make test         # the suite
make verify       # fmt-check, check, test, build — the whole local gate
make install      # into $PREFIX/bin, which defaults to ~/.local
```

`c`, `r`, `t`, `v` are aliases for compile, run, test and verify. `make clean` removes every build
output; `make tidy` syncs go.mod; `make release --type patch` cuts a version.

At an oslo prompt in this directory `make` is enough; everywhere else it is `oslo make`.

The toolchain comes from the flake — `nix develop`, or `direnv allow` and it is loaded on `cd`.

Nothing stops you calling the go tool directly, and one thing needs it: there is no recipe for
another architecture, so a cross build is spelt out.

```console
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -o drop-arm64 ./src
```

`CGO_ENABLED=0` matters for more than size: cgo would link the system resolver and pin the binary
to the host it was built on. With it off the same command targets anything Go does, which is how
this is tested on two architectures at once.

### size

19 MB compiled, 27 MB without `-s -w -trimpath`. `-s -w` strips DWARF and the symbol table but not
`gopclntab`, which Go needs for panics and has no flag to remove. What is left is genuine code.

It was 32 MB on libp2p. Moving to iroh took it to 15 MB compiled: 682 packages became 307.

`make build` does **not** compress: it copies the compiled binary into place, because this is one
that gets run rather than shipped over a wire that charges by the megabyte. The release workflow
packs with `upx -9`, which takes it to 6.8 MB and costs 0.054s of startup against 0.003s — worth it
for something downloaded once, not for a command you run in a loop. The compression *level* is
free: `-1`, `-5` and `-9` all unpack in the same time, so if you pack at all, pack hardest.

## commands

There are two ways in, and they reach the same node over the same protocol — everything below works
whichever of these you are looking at.

```
drop path ls beta     the command line: what beta shares with you
drop                  a full-screen interface: enter a device, then a path
```

An address is whose machine, which machine, and what on it. The three parts read from the right,
so leaving one out leaves out the one on the left:

```
bob:laptop:/chat   bob's laptop, its /chat
laptop:/chat       the machine called laptop
bob::/chat         bob, whichever machine of his answers
/chat              this machine
bob:laptop         the machine itself
laptop             a machine itself
```

Everything else is grouped by the noun it is about:

```
drop connect <address> [args]  open whatever is at an address
drop serve                     serve what the config declares, and stay reachable
drop                           all of it, in a full-screen terminal
                               the first device in the list is this one
                               paths nest: enter walks in, esc walks out
                               p shows a pairing code, t takes one
                               a conversation scrolls with the wheel, ↑↓, pgup/pgdn

drop file ls <address>                what is in a directory somebody serves
drop file get <address> [into]        copy one file out of it
drop file put <address> <file>...     copy files into it; - is standard input
drop file rm <address>                remove a file, or an empty directory
drop file mkdir <address>             make a directory
drop file mv <address> <to>           move something inside it

drop peer pair [ticket]        link a machine to this one; --qr to show a code
drop peer ls                   the machines this one knows
drop peer trust <name>         say you would show this person things without thinking
drop peer forget <name>        drop a machine from the address book
drop peer whois <name|id>      what this machine knows about another

drop path ls [address]         what a machine serves, and to whom
drop path join <address>       hold a namespace somebody else holds
drop path grant <path> <who>   let somebody reach a path
drop path revoke <path> <who>  stop them
drop path grants               what has been allowed and refused
drop path requests             who has asked to be let in
drop path ask <address>        ask to be let into a path you can see
drop path share [dir]          take a file from somebody, once: a path is up for as
                               long as the command runs, and gone once something lands
drop path cast                 serve a terminal read from stdin as asciicast

drop me id                     this machine's identity
drop me machine                what names this machine, and what would change it
drop me machine rebind         stop using a written-down key, be named by the hardware
drop me machine migrate <id>   say this machine became another one
drop me machine took <line>    on the new machine: take that statement up
drop me user                   who this machine belongs to
drop me vault                  whether what is kept on this disk is encrypted
drop me passwd                 hash a password, to guard a path with
drop me log [name]             a conversation, or all of them
```

## namespaces and archetypes

One identity per machine, and named paths under it. An address is a machine and a path:

```
   workstation:/inbox
   workstation:/inbox/photos
   workstation:/stream/of/one/specific/namespace
   ╰─────┬────╯╰──────────────┬───────────────╯
      who                   what
```

Each of those paths is a **namespace**: an address, an access rule, and the name of the
**archetype** it belongs to. The archetype is what the namespace *means* — what opening it does,
which settings it reads out of its declaration, what it hands whoever is on the other end.

```
   archetype   chat        what a chat is: messages, stored, acknowledged
   namespace   /friends    one of them
   namespace   /work       another one, same archetype, different rule and history
```

A namespace knows which archetype it belongs to and nothing about what that archetype means. That
is the whole shape of the thing: a chat, a terminal and a drop box have no code and no data model
in common, and the part of drop that resolves a path, checks a rule and frames the wire has never
heard of any of them. Adding a seventh is [writing one and registering it](#adding-an-archetype),
and nothing else.

In a config the archetype is named by `type`, and everything else in the table is that archetype's
own business:

```lua
drop.mount("/inbox", { type = "share", dir = "~/Downloads" })
drop.mount("/term",  { type = "tty",   shell = "/bin/sh", input = false })
```

Which archetype a path belongs to is declared here, on the side that serves it, and not chosen by a
flag at the far end — so there is one verb:

```
drop connect laptop:/inbox report.pdf  a share namespace, so this sends a file
drop connect laptop:/inbox -           and - is standard input, whose length is unknown
drop connect laptop:/logs              a stream namespace, so this reads it
drop connect laptop:/term              a tty namespace, so this attaches to it
drop connect laptop:/chat "on my way"  a chat namespace, so this is a message
drop connect laptop:/chat              with nothing to say, so this is the window
drop connect laptop:/open https://…    a link namespace, so that opens over there
drop connect laptop:/work              a files namespace, so this lists it
```

How it decides is one lookup: connect asks the machine what it serves, finds the archetype at the
path, and looks that name up in a table of how to open each kind from a terminal. A kind this
build has never heard of is named and refused, rather than half-opened.

Asking a namespace for something it is not is refused with the reason, rather than half-working:

```
$ drop connect laptop:/chat report.pdf
drop: 12D3KooW… declined: /chat is a chat namespace
```

### the archetypes

Six of them, and each one reads its own settings out of the mount that declares it:

| archetype | what it is for | it reads | what the far end gets |
| --- | --- | --- | --- |
| `share` | a drop box: things are pushed in, once | `dir` | it offers items, they land in `dir`, the session ends |
| `files` | a folder: the far end walks it | `dir`, `writable` | list and download; with `writable`, also upload, delete, mkdir, move |
| `chat` | a conversation | | it sends messages, stored here and acknowledged once they are on disk |
| `link` | a URL handed over | `action` | it sends a URL, which is written down, or given to `action` |
| `tty` | a terminal, shared | `shell`, `input` | one shell fanned out to everybody watching; typing only with `input` |
| `stream` | a command being followed | `command` | whatever the command writes, for as long as it writes it |

Anything a mount does not say takes the archetype's own default, and the two that hand something
over — `writable` and `input` — default to **off**. A mount may also pin `version` to one revision
of an archetype's protocol; without it, the newest this build has is used.

A declaration is read by the archetype it names, when the config is read. So a `share` with no
`dir`, or a `type` this build has never heard of, is an error with a file and a line on it, rather
than a namespace that turns out to answer nothing months later.

Each archetype also says whether one namespace of its kind is something several machines may hold
between them. `chat` and `files` say yes; `share`, `link`, `tty` and `stream` say nothing, and
declaring one of those [`shared`](#a-namespace-several-machines-hold) is refused where it is
written.

### a namespace several machines hold

Most namespaces are one machine's own. A terminal is somebody's screen and a drop box is somebody's
folder, and there is no sense in which two of them are the same one. Some are not: a conversation
and a folder are things several people may be changing at once, and then all of their machines are
holding one thing rather than several things that share a spelling.

A mount says so with `shared`:

```lua
drop.mount("/notes", { type = "chat", access = { "bob", "carol" }, shared = true })
```

Or, without editing anything:

```
drop path create /notes chat --access bob,carol --share --keep
```

What everybody calls it is worked out rather than issued: it is a hash of who made it, the path
they made it at, and a word telling one thing at that path from another made there later. So every
machine given those three facts arrives at the same name without anybody asking a server, and a
config read again after a restart names the same thing it named before. Writing a different word —
`shared = "second"` — is how you say this is a new thing at an old path.

**Who else holds it is the access rule.** There is no membership list: whoever the rule admits may
hold it, and widening the rule widens the set. Which also means it is each machine's own answer —
yours takes changes from the people *your* rule names, so removing somebody stops their next change
rather than unsaying their last one.

**Nobody is invited.** It turns up in `drop path ls` on their machine because their rule names you,
and you say yes to it:

```
$ drop path ls bob
  /notes  chat   messages, kept as a conversation  · shared, `drop path join` it

$ drop path join bob:/notes

/notes is held here  →  chat, shared

  also held by  carol
  history       4 changes came over
  reachable by  bob
```

It is written down, so it is here after a restart. Who may reach it *here* is this machine's own
decision — joining names the person you joined from, and `drop path grant` names anybody else.

From then on the machines keep each other level: each tells the others the changes nothing of its
own comes after, and is sent whatever it has not seen. That happens on a connection arriving, on a
change being made, and on a timer, because running it when there is nothing to say costs a few
identifiers. A change from somebody your rule does not admit is refused, whoever relayed it.

### share and files are not the same thing

They both point at a directory and they are opposites.

**`share` is one-shot and one-directional.** Somebody pushes items, they land in `dir`, the session
ends. Nothing is listed and nothing is asked for: the sender says what it has, the receiver says
how much of each it already holds, and the bytes go over. Whoever the access rule admits can *put*
things there, and cannot see, read or remove what is already in it — not even what they sent
themselves. A name that is taken becomes `report-1.pdf`, so nothing that arrives replaces anything.

**`files` is a folder the far end walks.** It lists directories, and it downloads. With
`writable = true` it also uploads, makes directories, moves things, and **deletes them**. There is
no separate delete permission: one flag turns on the whole writing half.

```lua
drop.mount("/inbox",  { type = "share", dir = "~/Downloads/drop" })  -- they may put things in
drop.mount("/papers", { type = "files", dir = "~/papers" })          -- they may read it
drop.mount("/scratch", { type = "files", dir = "~/scratch", writable = true, access = { "me" } })
```

Read the third line as: every machine of mine may take anything out of `~/scratch` and delete
anything in it. That is what `writable` means, so write it against a rule you would be happy to say
out loud, and leave it off otherwise — a folder that is read-only is the useful default and the
safe one.

> **Upgrading:** `files` used to be the drop box, and is not any more — the drop box is now called
> `share`. A config written before this that says `type = "files"` still loads, still points at the
> same directory, and now means *let them walk it and read it* instead of *let them send here*.
> Read your `init.lua` again before you next serve it. There is no compatibility shim and there
> will not be one; the names mean what this table says they mean.

### adding an archetype

Six is what is registered today, not a ceiling. An archetype is an implementation of one
interface — [`arch.Archetype`](src/pkg/arch/arch.go) — and a line registering it:

```go
Name() string                                  // what a config writes, and what travels
Version() int                                  // which revision of your own protocol that is
Read(arch.Declared) (arch.Config, error)       // your settings, out of the mount that declared you
Note(arch.Config) arch.Note                    // what a listing may say about one of yours
Serve(ctx, arch.Session) error                 // one session, over a stream nothing else reads
```

`Read` is handed the mount table as written, and pulls its own keys out of it by name; the config
reader knows none of them. `Note` is how a listing, a row in the interface and the startup table
describe your namespace without a case for it. `Serve` is given a framed connection and the path,
and what is said on it from there is yours alone — no other part of drop reads a byte of it.

Register it in [`src/cmd/archetypes.go`](src/cmd/archetypes.go), which is where a process says
which archetypes it answers for — the daemon registers all six, a chat window registers one, and a
test registers whatever it is testing. Nothing in `ns`, `conf`, `proto` or `wire` changes, and
nothing in them may be made to: a path that resolves an access rule and a wire that frames bytes
have no business knowing what your archetype is.

[`src/pkg/arch/whole_test.go`](src/pkg/arch/whole_test.go) is that claim, tested. It is a `camera`
archetype written entirely inside one test file — declared in lua, read into settings of its own,
mounted, listed, opened over a pipe and served, speaking a protocol nobody else has heard of. Read
it as the shortest complete answer to "what would mine have to look like".

### an archetype written in lua

You do not have to rebuild drop to add one. A file in `archetypes/` beside your `init.lua`
declares an archetype, and it is registered in the same registry as the six, under the same
interface, before the config that mounts it is read:

```lua
drop.archetype{
  name    = "camera",
  version = 1,
  shape   = "note",                                 -- optional; see below
  read    = function(d) return { device = d.device } end,
  note    = function(c) return { detail = c.device, glyph = "◉" } end,
  serve   = function(s, c) s:write("looking at " .. c.device) end,
}
```

`read` is handed the mount table — `d.device` is whatever `device =` said, and a setting the config
never wrote is `nil` — and returns that namespace's settings, which come back as `c` later. It runs
while the config is being read, so `error("a camera needs a device")` is a mistake with a file and a
line on it. `note` is what a listing may say. `serve` is one session, and it is straight-line code:
the waiting happens outside it.

| in a session | what it is |
| --- | --- |
| `s:read()` | the next frame and what kind it was; nothing at all once the far end has finished |
| `s:write(bytes[, kind])` | a frame back; `item` unless you say otherwise |
| `s:who()` | who is calling: `id`, `name`, `label`, `person`, `paired`, `trusted` |
| `s:path()` | the namespace this is |
| `s:open(name[, "r"\|"w"\|"a"])` | a file in this namespace's own directory; `:read([n])`, `:write(b)`, `:close()` |
| `s:run{ "program", "argument" }` | a process, its output read back, killed with the session |
| `drop.log(text)` | a line in the daemon's output, attributed to the plugin |

`s:open` takes a **name and never a path**. It is resolved inside a directory drop holds open for
that one namespace, one component at a time, and leaves it for nothing — not by climbing and not by
following a link already there. Nothing else on this disk is reachable, and neither is `io`, `os`,
`debug`, `package`, `require`, `load` or the global table: a plugin's world is built by listing what
goes into it. Each session gets a runtime of its own and a budget of its own, and a session that
spends it — a loop that never ends, a table that never stops growing — is stopped, alone, with the
daemon still standing.

`shape` is for the other machine. Two machines need the same file to speak an archetype nobody else
has heard of; naming a `shape` says *and if you have not got it, this sounds like a note* — so a
stock peer opens it with the note opener, draws it with the note glyph, and is served by your
plugin, which is all it ever needed. Say nothing and yours is unopenable from anywhere it is not
installed, which is honest and sometimes the point.

[`misc/archetypes/camera.lua`](misc/archetypes/camera.lua) is a whole one, and
[`src/pkg/arch/spoken_test.go`](src/pkg/arch/spoken_test.go) is the same claim tested: a camera in
lua, declared in a config, mounted, listed, opened over a pipe and served, with `ns`, `conf`,
`proto` and `wire` containing not one line that knows it exists.

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
before you know who is coming. `drop me passwd` prints the hash to put in the config — the
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
drop.mount("/vault", { type = "share", dir = "~/vault", visible = "paired" })
drop.mount("/work",  { type = "chat", access = { "bob" }, visible = { "carol" } })
```

`visible` is its own option rather than a rung inside `access`, because it answers a different
question: access says who gets in, visible says who is told there is a door. A path can have both —
shared with one person, merely visible to another, who then asks.

```console
$ drop path ls beta
  /vault   share    locked

$ drop path ask beta:/vault --why "for the thing we discussed"
asked beta for /vault
nothing is granted by asking: somebody there decides.
```

On the other machine the request waits until somebody answers it:

```console
$ drop path requests
  /vault
    from  carol
    why   for the thing we discussed

$ drop path requests allow /vault carol
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
$ drop path grant  /work carol@laptop     # on top of whatever the config says
$ drop path revoke /work bob@phone        # against it
$ drop path revoke /work bob@phone --forget
$ drop path grants
```

The interface does the same thing: on one of your own paths, `w` opens **who may reach it** --
people, machines, and the rules that name nobody -- and `a` and `x` allow and refuse from there.

A refusal beats every rule there is, including one you wrote, and takes effect on the **next
connection** rather than when a badge expires. It is local: this machine stops trusting them, and
nobody else is told. Grants live in `$XDG_CONFIG_HOME/drop/grants.json` and cover everything under
the path they are written at, so refusing somebody at `/` refuses them everywhere.

### listings are filtered, not refused

`drop path ls beta` shows what beta shares **with you**. A path shared with someone else is absent,
not marked refused: a listing that showed the whole tree would tell someone which machine has
a terminal worth attacking.

A path guarded by a password is in no listing at all — nobody offers a secret to ask what
exists — so whoever you hand one to needs the path as well as the word.

## who you are

A device has an identity of its own — the endpoint key, which QUIC proves on every connection. That
says *which machine*, never *whose*. A **user key** says whose. Where the endpoint key itself comes
from is [what names a machine](#what-names-a-machine), below.

```
  you ──── one SSH key ────┬──── laptop     each machine carries a badge:
                           ├──── yubikey    "this user owns this machine,
                           └──── server      called <name>, until <date>"
```

An ed25519 SSH key, generated on first run if you do not point drop at one, and read through
`ssh-agent` if you do — so a key on a YubiKey, in a PIV slot, or in a file all work the same way.
`drop me user` shows what this machine is using.

```console
$ drop me user
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
drop peer pair <ticket>             the user key is learnt; their other machines work later
drop peer pair <ticket> --machine   this machine and no other
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

### what names a machine

A user key says *whose*. What says *which machine* is the endpoint key — and where that comes from
decides two things you care about: whether a machine survives being wiped, and whether two people
with accounts on one box are one machine or two.

drop takes it from the machine itself, strongest source first, and says which one it got:

```console
$ drop me machine
5629e48c2227fc04ac3fc875a85569bb89e66123699ee9dd20552e0b421c044d

  named by      the serial on the drive the system is on
  which reads   e5535c77824b
  and by        where this drop keeps its things, so every account and
                profile here is reachable as itself
  survives      a reinstall, because nothing about it is written down
  changes if    the drive the system is on is replaced, or this drop is moved to another home
```

| source | what it is | what changes it |
| --- | --- | --- |
| the TPM | a key the chip derives and never hands out | clearing the TPM |
| the board | the serial the firmware holds — DMI on a PC, the device tree on a board | replacing the board |
| a drive | the serial on the drive `/` is on, followed down through partitions, LVM and encryption to the metal | replacing that drive |

The TPM row is written and **has never run** — neither machine this was built on has a TPM, and
code that has not executed is not code that works. A PC whose firmware offers one (Intel PTT, AMD
fTPM) usually ships with it switched off in the BIOS. `metal.Seal` and `metal.Unseal` are there for
the same chip to encrypt with later, and carry the same warning.

Nothing is written down when a machine will name itself, which is the point: **reinstall and it
comes back as the machine it was**, with no backup and nothing to restore. Carry a backup to
another box and it does *not* come back as that machine, because the key was never in the backup.

The seed is `KDF(purpose, what the hardware said, where this drop keeps its things)`. The second
half is in there because a machine is one machine and the drops running on it are several — every
account with one, every profile under an account, every node a test brings up. Each has to be
reachable as itself, and two of them deriving one key would not be one machine with two people on
it, it would be two programs answering to one address, which is nobody.

Where a drop keeps its things is what makes it a different drop, and it is stable in exactly the
way that is wanted: the same path comes back on the machine that comes back, and it does not travel
to another one. Same place after a wipe: same name. Two accounts, or two profiles, or two test
nodes: different names. Same place on another box: a different name, because the hardware differs.

**What this is not.** On a machine without a TPM the serial is one every account there can read, so
every account there can derive the machine key. Machine identity locates *hardware*; it is never a
wall between the people sitting at it. Who somebody is stays with their user key, which is where
the namespaces are owned anyway.

A machine that has been running with a key of its own **keeps it**. Deriving a different one on an
ordinary upgrade would break every pairing that names that machine without anybody asking, so a
machine already installed says so instead, and nothing changes until you say it should:

```console
$ drop me machine
5629e48c2227fc04ac3fc875a85569bb89e66123699ee9dd20552e0b421c044d

  named by      the key kept in /home/bresilla/.config/drop/identity
  survives      a reinstall only if that file is in your backup

  this machine could name itself instead, by the serial on the drive the system is on.
  `drop me machine rebind` changes it over — and every pairing that
  names this machine has to be made again.
```

Changing over is deliberate, and says what it will cost before it does anything:

```console
$ drop me machine rebind
this machine is 2e0b421c044d
it would become 9983d3d10ca5, named by the serial on the drive the system is on

every machine paired with this one knows it by the old name and will not
recognise the new one. Each pairing has to be made again.

run it again with --yes to go ahead.
```

The old key is kept beside the new one as `identity.was`, not deleted.

### one machine, several people

Each account on a machine has its own endpoint key, so each is reachable as itself. What ties them
together is a **plate**: a statement signed by the *machine* key — the one every account there
derives — saying "this endpoint is one of the drops running on me, and it belongs to that account".

```
  one machine ──── machine key ────┬──── alice's drop    "endpoint E is on me,
                                   └──── bob's drop       account alice"
```

It rides on every connection next to the badge and answers a different question. The badge says
whose drop this is; the plate says what it is sitting on. A peer learns that two endpoints it can
reach are two people at one machine, rather than two unrelated machines that happen to answer at
the same time.

Because every account on a machine can produce that machine's plate, **no access rule is satisfied
by it**. It is there so a listing can say what is where, and for nothing else.

`$DROP_PROFILE` below works the same way — it keeps its things somewhere else, so it gets a name of
its own and is a stranger to the account it runs beside, which is what it is for. Two real accounts
on one machine need nothing set up at all.

### moving to another machine

A name taken from the hardware stays with the hardware. That is the point of taking it from the
hardware, and it is also the one thing that has to be possible anyway — machines get replaced, and
everybody who knew the old one should end up knowing the new one without pairing all over again.

So the old machine says it, while it still can, and signs with the key everybody already knows it
by: **its own endpoint key**. Nothing new has to be trusted, because the old name *is* the key that
signed the statement.

```console
# on the new machine
$ drop me id
39ce1a4bee9e4275e641045f9b867c6f60dbd4e801f1719029fc1ba8a4ed1a91

# on the old one, while it still runs
$ drop me machine migrate 39ce1a4bee9e…
2e0b421c044d is now 1ba8a4ed1a91

drop1AMRkcm9wLWhhbmRvdmVyLzEKd2FzIDU2MjllNDhj…

# back on the new machine
$ drop me machine took drop1AMRkcm9wLWhhbmRvdmVy…
  2e0b421c044d said it became this machine
  account   bresilla
  good till 2026-09-03 15:04
```

Restart drop on the new machine after `took`: the handover is picked up when it puts its badge on.

After that the new machine carries the handover on every connection it makes — there is no telling
which peers have heard yet — and each of them points what it had filed under the old name at the
new one. The entry keeps **the local name, the shared secret, whose machine it is, and whether it
is trusted**; only the addresses go, because the machine is somewhere else now and they are learned
again on first contact. Nobody pairs again.

```console
# on a peer, in the daemon log
tron is now 1ba8a4ed1a91, and was 2e0b421c044d
```

A peer that has already heard does nothing with it. It travels on a `hello` as well as on an open,
because `drop path ls` is how most peers hear from a machine first.

Two things have to hold before a peer acts on one, and the second is the one that matters. It has
to be signed by the machine it says it was. And it has to name **the very caller presenting it** as
what that machine became — otherwise anybody who overheard a handover could present it and be taken
for somebody else's machine. It runs out after seven days, names exactly one successor, and moves
one account.

A move onto an id already in the book is refused: that would make two entries one.

Both ends need the same build. The opening frame grew these claims, and a drop that predates them
reads a newer one as `malformed unsigned varint` — there is no compatibility shim anywhere in drop,
by design.

### another person, same machine

`$DROP_PROFILE` is a whole other identity: its own device key, user key, address book and
conversations, under `…/drop/profiles/<name>/`, on a port derived from the name so two can run at
once. Two profiles are strangers who must pair, which is how a rule that names somebody else gets
tried without a second computer.

```console
$ DROP_PROFILE=bob drop me user   # a different person, called tron-bob
$ DROP_PROFILE=bob drop serve     # alongside your own, on its own port
$ drop peer pair                  # then pair them, as you would two machines
$ DROP_PROFILE=bob drop peer pair <ticket>
```

A profile that sets `drop.user_key` to the same key you use is *you* again — leave it out of a
profile's config for a genuinely separate person.


**Revocation** is expiry and a local refusal, and nothing more honest is possible without a server.
A badge lasts ninety days, so a lost machine stops being trusted within ninety days rather than
today; `drop peer forget bob@laptop` stops this machine trusting it immediately, and tells nobody
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
drop.direct = false     -- optional; on unless you say otherwise, see below

-- A branch: no archetype, just who may reach everything under it. Access inherits downward, and
-- a path with no rule above it is reachable by nobody.
drop.mount("/work",    { access = { "laptop", "phone" } })
drop.mount("/friends", { access = { "bob", "carol" } })

drop.mount("/work/inbox", { type = "share",  dir = "~/Downloads" })
drop.mount("/work/logs",  { type = "stream", command = "journalctl -f -n 50" })
drop.mount("/work/term",  { type = "tty",    shell = "/bin/sh", input = false })

-- A folder they walk. Read-only, because writable would also mean they may delete from it.
drop.mount("/work/notes", { type = "files",  dir = "~/notes" })

drop.mount("/friends/chat", { type = "chat" })
drop.mount("/friends/open", { type = "link", action = "xdg-open" })

-- A deeper rule replaces the one above it rather than adding to it.
drop.mount("/friends/just-bob", { type = "share", dir = "~/bob", access = { "bob" } })

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
somebody who would rather a device be unreachable than announced at all, and `drop.direct = false`
keeps announcing but leaves this machine's own addresses out of what is announced.

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
  - The record holds where this device can be reached — its relay, whatever a relay saw it
    arrive from, and the addresses it has on its own networks. That last part is what lets two
    machines on one wire or one overlay meet over a link that answers in milliseconds instead of
    through a relay in another country, and it is what `drop.direct = false` turns off. It costs
    something: a record says `192.168.1.24`, or whatever a VPN gave this machine, so whoever can
    read it learns which networks this device is on and can watch them change as it moves. For a
    rendezvous record that is the one device you paired with, and nobody else — the record is
    filed under an identity only the two of you can compute. While a device is offering to pair
    it also publishes under its own id, and there anybody holding the ticket can read it.

The cost is that connections may cross a relay, and the relay knows two parties are talking
even though it cannot tell who they are or read anything. Traffic stays end-to-end encrypted.
Set `drop.relays` to your own if you would rather not use the defaults.

Mounts are keyed by path, so declaring one twice replaces it rather than adding a second — a config
that loops, or is re-read, cannot silently grow the table.

A file that exists and does not parse is **fatal**, and the error names the file and line, whether
what is wrong is the lua or a setting an archetype refused. A typo that silently drops half the
namespaces is worse than not starting. With no file at all, drop serves a small default: `/inbox`
to send to, `/chat` to talk in, `/open` for links — and nothing that hands over a directory, runs a
command or shares a terminal, because those are decisions.

`drop path ls` prints what this machine serves, and what each archetype says about itself.
[`misc/init.lua`](misc/init.lua) is a worked example with one namespace of every archetype in it.

## conversations

Everything drop does is a **conversation with an endpoint id**. Files, chat, links, terminals and
endless streams are modalities inside it, not separate features that happen to share a binary:

```
drop connect laptop:/chat               # talk
drop me log laptop                      # the whole story, in one place
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
whether it was said, so sending never fails because the far end is asleep — it queues, and goes
out when the device appears. a chat window retries in the background while you keep typing.

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

The machine's own key is often **not** on disk at all — see [what names a machine](#what-names-a-machine).
`~/.config/drop/identity` exists only on a machine that would not name itself, or one that has been
running since before it could. Beside it, `handover` is the statement a machine presents after
moving; it is not a secret, it is a thing anybody may check and only the old machine could have
made, and it is thrown away once it runs out.

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
drop me vault        what it is doing, creating nothing
drop me vault seal   encrypt what is already on this disk
drop me vault clear  put it back in the clear
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

### the opening, and then whatever the archetype says

Only the first two frames are everybody's. A caller says which path it wants and which archetype it
expects to find there, along with what it can say about itself; the far end resolves the path,
checks the rule, and either accepts or gives a reason:

```
  Open{path, archetype, version, badge, plate, handover}  ------->
                                   <------- Accept     // or Reject{reason}
```

Three separate claims ride in that frame, and they answer three different questions. The **badge**
says whose machine this is, signed by a user key. The **plate** says what machine the drop is
running on, signed by the machine key. The **handover** says this caller is what some other machine
became, signed by that other machine's key. Each is checked against the endpoint QUIC already
proved, and any of them failing yields a caller less is known about rather than a refused
connection — an expired badge is not a device that vanished.

After that the stream belongs to the archetype, and nothing generic reads another byte of it. Four
shapes are in use, and a seventh archetype is free to invent a fifth.

**A push** — `share`:

```
sender                          receiver
  Item{names, sizes}   ------->        // the whole offer, in one frame
        <------- Accept{resume[]}      // per item, bytes already held
  Data ... Data        ------->        // then each item in turn
  End{size, digest}    ------->
        <------- Ack{ok}               // hashed and verified
```

**A batch** — `chat` and `link`: what is acknowledged is what reached a disk, nothing more:

```
  Item{message}        ------->        // as many as there are
  End                  ------->
        <------- Ack{the ids stored}
```

**Rounds** — `files`, one request and one reply at a time, for as long as the caller keeps asking.
A round that moves a file ends the way a push ends, with a size, a digest and a verdict:

```
           <------- Reply{writable}     // what this mount allows, said once
  Request{list /papers}   ------->
           <------- Reply{entries}
  Request{get thesis.pdf} ------->
           <------- Reply{size}, then Data … End, and an Ack back
```

**A duplex** — `tty` and `stream`: both ends writing at once, nobody counting:

```
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
drop connect laptop:/term      open it; what you type goes there if it takes input
```

A **cast** is the terminal you are already sitting at, shown to whoever is watching. It reads
asciicast v2 on standard input, so anything that writes asciicast will do:

```
asciinema rec --stdout | drop path cast
HEXE_SHARE_BACKEND="drop path cast" hexe ...

drop connect laptop:/cast      watch it
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

### joining a terminal already running

What a watcher is handed on joining is the **screen**, not a replay of recent bytes. A full-screen
program draws by moving the cursor and changing the cells that altered, so a tail of output holds
whichever cells happened to change lately — join btop halfway and you get those and nothing else: a
screen with holes in it, filling in slowly as the program repaints.

So the serving side keeps a terminal of its own, feeds every byte through it, and hands a joiner the
picture as it stands. However long the program has been running, that is one screenful.

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

Without a daemon, `drop connect laptop:/…` only reaches the laptop while somebody there is
running something that serves.

## layout

```
src/main.go            entry point; the version lives here
src/cmd/               the cobra command tree, one file per command
src/pkg/node/          identity, the iroh endpoint, relays
src/pkg/metal/         what machine this is, off the TPM or a serial, and TPM sealing
src/pkg/plate/         a machine vouching for the drops on it, and for what it became
src/pkg/discovery/     finding a device on this wire
src/pkg/rendezvous/    finding one that moved, under a derived identity
src/pkg/ns/            namespaces: paths, the access rules on them, nothing about meaning
src/pkg/arch/          archetypes: the interface, the registry, and one package each
src/pkg/passwd/        argon2id, for the secrets that guard a path
src/pkg/proto/         pairing, hello, opening a namespace, and the framing under them
src/pkg/wire/          the binary encoding under all of it: varints, no reflection
src/pkg/dial/          turning a device you know into a connection to it
src/pkg/book/          the address book, including pairing secrets
src/pkg/grant/         who has been let in and shut out from the interface
src/pkg/asked/         requests to reach a path, waiting on an answer
src/pkg/vault/         the key everything on this disk is encrypted with
src/pkg/seen/          devices that dialled and were turned away
src/pkg/shares/        what each device last said it shares
src/pkg/user/          a person, and the badge each of their machines carries
src/pkg/convo/         the durable conversation log and outbox
src/pkg/history/       what happened to one thing: signed changes, causally ordered
src/pkg/weave/         putting versions of one thing back together, three ways at a time
src/pkg/among/         who else holds a namespace, read off its access rule
src/pkg/meet/          two machines catching up on one thing: heads, and what is missing
src/pkg/live/          a stream both ends write on at once
src/pkg/term/          a terminal screen, rebuilt from what a device sends
src/pkg/cast/          one terminal fanned out to many watchers
src/pkg/asciicast/     reading an asciicast stream
src/pkg/ticket/        a pairing invitation, as text, link, or QR
src/pkg/tui/           the full-screen interface
src/pkg/conf/          the Lua configuration
src/pkg/made/          the namespaces put up from the command line
src/pkg/nudge/         hearing a file change, so a save is not waited for
src/pkg/keep/          writing a file so a reader never sees half of one
misc/                  the systemd user unit, and an example config
```

## one node, however many commands

`drop serve` holds a connection to every device you have paired with, and every command borrows
them over a socket on this machine rather than standing up a node of its own.

The difference is the whole cost of reaching somebody: a rendezvous lookup, a relay session and a
handshake — seconds — against a stream on a connection that already exists.

```
drop path ls laptop   55ms with a daemon running, seconds without
```

The same socket carries `drop path cast` and the pairing offer, for the same reason: two processes
cannot share one address, so the one holding it does the work.

With nothing running, every command dials for itself, exactly as before.

## testing it

`make test` is the unit suite. Two more are separate, because each builds the binary, starts
daemons and takes a minute — that does not belong in the run you do on every save.

```
make test         # the suite
make test-all     # the same, with the race detector
make e2e          # two real nodes on this machine
make cover        # the suite, with a coverage profile
```

**e2e** drives two nodes on this machine from the command line over QUIC: pairing, a message each
way, a file each way, standard input as a file, a link, a stream, a shell, a cast, and a message
queued for a device that was switched off.

**cross** is the third, and has no recipe because it needs a second machine — reachable over ssh
and running the same build. It is what proves a thing works between two architectures rather than
between two processes that happen to agree.

```console
go test -tags cross -count=1 -v ./src/e2e/
```

[`misc/orin.sh`](misc/orin.sh) is what puts the build on the other machine.

```console
misc/orin.sh deploy      # build for arm64, copy it over, restart the daemon
misc/orin.sh log 40      # what it said
misc/orin.sh me machine  # bare words run drop there
```

`$ORIN` is the host it uses.

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
