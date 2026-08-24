# What is kept, and who can read it

drop writes conversations to `$XDG_DATA_HOME/drop/convo/<peer id>/history` in the clear. The files
are `0600` under `0700` directories, which stops another account on the same machine and nothing
else. A laptop that is stolen, a disk that is pulled, a backup that goes somewhere it should not —
all of them hand over every message, every link, and the name of every file that changed hands.

The access rules do not help here. They decide what another *device* may reach over the wire. This
is about a disk that is no longer in your hands.

## What encryption at rest can and cannot do

It protects data on a machine that is **off**. That is the whole of it, and it is worth having:
stolen laptops, decommissioned disks and leaked backups are the ordinary way private things escape.

It does not protect a machine that is **running**, because drop can read the data — anything with
your session can ask drop, or read the same memory. Nothing filesystem-level fixes that, and a
design that claims otherwise is lying.

## The shape: one data key, wrapped to keys you already have

Not age per record: an age file carries a header per encryption, and there are thousands of records.
Envelope encryption instead, which is what age itself does internally.

**A data key.** Thirty-two random bytes, made once. Everything on disk is encrypted with it.

**Wrapped to recipients.** The data key is stored as `keys/data.age`, encrypted *to* one or more age
recipients — which is exactly what age is for, and what a YubiKey age identity is for:

```
age -r age1yubikey1…            \   # the key in your pocket
    -r age1…backup…             \   # a second one, kept somewhere else
    -o keys/data.age
```

More than one recipient, always. A data key wrapped only to a YubiKey is a conversation history that
dies with the YubiKey.

**Unwrapped once.** The daemon unwraps at startup — one touch — and holds the data key in memory
until it stops. Not per message: a chat that asks for a touch per line is a chat nobody uses.

**Records encrypted where they are.** The store is already append-only, one length-prefixed record
at a time. Each record's payload becomes XChaCha20-Poly1305 with its own nonce, and the peer id and
record id as associated data — so a record cannot be moved to another conversation or replayed into
one. Appending, reading and the outbox all keep working exactly as they do.

## What still leaks

**Who you talk to, and how much.** The directories are named after peer ids and their sizes are
visible. Encrypting the contents hides what was said, not that it was said.

That is fixable: name the directories `HMAC(data key, peer id)` instead. The cost is that without
the key, nothing can even tell which conversation is which — including `drop log` on a machine
whose key is not plugged in, which will look like the conversations are gone.

**Files that arrived.** Things sent to a files namespace land where the config says. Those are
deliverables somebody asked for, in a directory they chose. Encrypting them would be drop deciding
it knows better.

## Laptops and servers want different things

A laptop can ask for a touch at startup. A daemon on a headless server cannot: it has to come back
after a reboot at four in the morning with nobody there.

So the recipients are configuration, and the honest answer differs:

```lua
drop.vault = { "age1yubikey1…", "age1…backup…" }   -- a laptop: unreadable without the key
drop.vault = "~/.config/drop/vault.key"            -- a server: protects backups, not a live disk
```

The file case is worth having and worth being plain about: a key file next to the data it unlocks
stops a leaked backup and a resold disk, and stops nothing at all on a machine somebody already has.

## The pairing secrets are a harder question

`peers.json` holds the secrets that pairing established. Anybody who takes it can be you to every
device you have paired with — a worse loss than the message history it sits next to.

Encrypting it to a YubiKey is the strong answer and it means **drop cannot start without the key
plugged in**. That is right for a laptop and wrong for a server that is expected to answer at any
hour. It should be offered, defaulted off, and described exactly like this rather than sold as
simply better.

## Order of work

1. The data key, wrapped and unwrapped, doing nothing yet.
2. Records encrypted on write, both forms readable on read, so an existing history keeps working.
3. A command to encrypt what is already there, and one to undo it.
4. Directory names, once everything else is proven.
5. `peers.json`, opt in, with the startup cost said out loud.
