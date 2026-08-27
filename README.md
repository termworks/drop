# drop

Peer-to-peer file transfer, chat, folders and terminals. No account, no server holding your data.

Pair two devices once, by key. After that either can reach the other from anywhere — across NATs,
across networks, through address changes.

```console
drop peer pair                      # on one machine: prints a ticket
drop peer pair 9363f77d…#qxwo-e62y  # on the other: done, forever
```

---

## A path is a thing, and what it is is up to you

A machine serves **namespaces**: paths, each with a rule about who may reach it. What opening one
*does* is its **archetype** — and the two are kept apart on purpose.

```lua
drop.mount("/",        { access = "paired" })
drop.mount("/inbox",   { type = "share",  dir = "~/Downloads" })
drop.mount("/papers",  { type = "files",  dir = "~/papers" })
drop.mount("/standup", { type = "note",   file = "~/notes/standup.md" })
drop.mount("/logs",    { type = "stream", command = "journalctl -f" })
drop.mount("/term",    { type = "tty",    shell = "/bin/sh", input = false })
```

Seven ship. Adding one is writing an implementation and registering it — nothing in the namespace
layer, the config reader, the protocol or the wire gains a case. Write one in
[Lua](docs/lua.md) and both ends load the same file; a machine that does not have it opens the
namespace as whatever it says it *speaks like*, rather than failing.

## An address reads from the right

```
bob:laptop:/chat   bob's laptop, its /chat
laptop:/chat       the machine called laptop
bob::/chat         bob — whichever machine of his answers
/chat              this machine
```

That last-but-one line works because **pairing is with a person, not a machine**. Both sides write
down the other's user key, so a machine of theirs you have never met arrives with a name.

## A machine is named by the machine

Identity comes from the hardware — a TPM if there is one, otherwise a board or drive serial.

```console
$ drop me machine
  named by      the serial on the drive the system is on
  survives      a reinstall, because nothing about it is written down
```

Wipe the disk and it comes back as the machine it was, with no backup and nothing to restore. Carry
a backup to another box and it does *not*, because the key was never in the backup. Replacing a
machine is a [signed handover](docs/identity.md) the old one makes while it still can, so nobody
has to pair again.

## Some things several people change at once

A `note` is one file, edited by several people; a `files` folder is a directory several people work
in. Changes are signed, causally ordered, and merged three ways — the same shape `git merge-file`
uses, and checked against it with 30,000 randomised merges.

```console
$ drop path create /standup note --set file=~/notes/standup.md --share --keep
$ drop path join tron:/standup --at /standup --set file=~/notes/standup.md
```

drop never becomes an editor. It notices a save, signs what was saved, and writes back what all the
changes together make.

---

## Docs

One document per subject in [`docs/`](docs/README.md): how it works, what it cannot do, and where it
lives in the tree. Every claim in them was checked against the source or a running build, on two
machines of different architectures.

| | |
|---|---|
| [**Start here**](docs/getting-started.md) | a walkthrough: one identity across three machines, from nothing to talking |
| [How it is put together](docs/architecture.md) | the layers, one daemon, and what happens when somebody opens a path |
| [What names a machine](docs/identity.md) | identity from the hardware, several people on one machine, moving to another |
| [Namespaces and archetypes](docs/namespaces.md) | an instance, a meaning, and the rule that keeps them apart |
| [The wire](docs/wire.md) | frames, the opening, and the shape each archetype speaks afterwards |
| [The command line](docs/cli.md) | one noun per group, and an address that reads right to left |
| [The archetypes](docs/archetypes.md) | share, files, chat, note, link, stream, tty — what each is for |
| [Sharing a terminal](docs/terminal.md) | one shell and many watchers, the screen rather than a replay |
| [The interface](docs/interface.md) | people, then machines, then paths — and managing somebody |
| [One thing, several machines](docs/shared.md) | signed changes, merging, and how a history stays small |
| [An archetype in Lua](docs/lua.md) | a plugin both ends load, and the sandbox it runs in |
| [Access rules](docs/access.md) | the vocabulary, how it inherits, and what a refusal means |
| [Pairing](docs/pairing.md) | recognition against trust, badges, and being found without being findable |
| [On the disk](docs/storage.md) | where everything lives, the vault, and one writer at a time |
| [Hardening](docs/security.md) | what is bounded before anybody is let in, and what is not defended |
| [Configuration](docs/config.md) | `init.lua`, and the settings that are not namespaces |
| [Testing it](docs/testing.md) | three suites, fuzzing, and two real machines |

---

## Quick start

**Build.** One Go module; the build is [`.make.lua`](.make.lua), run with `oslo make`.

```console
make build        # the release binary, into ./drop
make test         # the suite
make verify       # fmt-check, check, test, build — the whole local gate
make install      # into $PREFIX/bin, which defaults to ~/.local
```

The toolchain comes from the flake — `nix develop`, or `direnv allow` and it loads on `cd`.

**Run.** `drop serve` keeps the node reachable; install it as a user service so the device is
reachable whenever it is on.

```console
install -m 0644 misc/drop.service ~/.config/systemd/user/
systemctl --user enable --now drop
```

**Use.**

```console
drop                            the interface: a person, a machine, a path
drop path ls bob:laptop         what they share with you
drop connect bob:laptop:/chat   open whatever is there
drop file get bob:laptop:/papers/thesis.pdf
```

With no config at all, drop serves a small default: `/inbox` to send to, `/chat` to talk in, `/open`
for links — and nothing that hands over a directory, runs a command or shares a terminal, because
those are decisions. [`misc/init.lua`](misc/init.lua) is a worked example with one namespace of
every archetype in it.

---

## Status

Discovery, pairing, transfer, shared folders and shared notes work, on amd64 and arm64.

- identity taken from the hardware, so a reinstall comes back as the same machine
- one machine, several accounts, each reachable as itself
- moving to new hardware without pairing again
- pairing over a one-time 60-bit code, hashed into its own rendezvous
- a private rendezvous per pair, rotating hourly
- transfer with blake3 verification, resume, and paired-only acceptance
- a push carries files, not whole directories, yet

The TPM path is written and **has never run** — neither machine this was built on has one. See
[what names a machine](docs/identity.md).
