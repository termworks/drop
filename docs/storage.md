# On the disk

Everything drop remembers is a small file in one of two places. Nothing is a database, and every one
of them can be read with `cat` if it is not sealed.

```
$XDG_CONFIG_HOME/drop/          ~/.config/drop
  init.lua          the configuration
  archetypes/       plugins, loaded at startup
  identity          this machine's key — only when the hardware will not name it
  identity.was      what it used to be, kept by `drop me machine rebind`
  handover          a statement this machine presents after moving
  user, user.pub    the user key, when drop keeps one of its own
  badge, badge.sig  this machine's badge and its signature
  peers.json        the address book, including pairing secrets
  paths.json        namespaces put up from the command line with --keep
  grants.json       what has been allowed and refused from the interface
  vault.key         the data key, wrapped to whoever you named

$XDG_DATA_HOME/drop/            ~/.local/share
  convo/<peer id>/  conversations and the outbox
  history/<id>/     the signed record of one shared thing
```

`$DROP_PROFILE` puts the whole config tree under `…/drop/profiles/<name>/` and the data tree
likewise, which is why two profiles are strangers.

**`identity` is often not there at all.** A machine that names itself from its hardware writes
nothing down — that is the point of [taking the name from the machine](identity.md). The file exists
only where the hardware will not answer, or on a machine that has been running since before it
could.

## One writer at a time

The address book, the grants and `paths.json` are each shared by every drop on the machine — the
daemon, the interface, and each `drop peer pair` or `drop path create` you run. Read, change, write
is three steps, and a second writer landing between the first and the third has its change thrown
away by the third: a pairing that never happened, a grant that was made and is gone, with nothing to
say so.

Each of those changes takes the file to itself first, so the three steps are one. That mattered when
the only thing that wrote was somebody typing; it matters more now that a machine
[saying it moved](identity.md) makes the daemon write, because then the moment is chosen by
somebody else.

Nothing is written in place. A scratch file beside the target takes the bytes, is flushed to the
disk itself rather than to the kernel's opinion of it, and is then renamed over the target, which is
one atomic step. The directory is flushed too, because a rename the disk has not been told about is
a rename a crash undoes.

## The vault

Without one, conversations are written in the clear and `0600` under `0700` stops another account on
the same machine and nothing else.

```console
$ strings ~/.local/share/drop/convo/d04c…/history
hey, this is from the terminal
```

A vault changes that. One data key — thirty-two random bytes — encrypts every record; the key itself
is written once, encrypted to whoever you name, and unwrapped once at startup. A touch per message
would be unusable; a touch per start is not.

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

Both walks read every record and write it back — sealed or not — so turning a vault on does not hide
what came before it, and turning one off is not a loss. Stop drop first: a message that lands during
the walk is in neither file afterwards.

The same key seals the signed records of shared things, because a note somebody wrote is no less
theirs than a message they sent.

The data key is made **exactly once**, however many drops start at the same moment. Two of them each
minting one would leave whichever wrote second owning the file while the other went on sealing
records with a key that is nowhere — and a record sealed to a key that was never written down is not
recoverable by anybody, ever.

**What it protects:** a machine that is off. A stolen laptop, a pulled disk, a leaked backup. That is
the ordinary way private things escape.

**What it does not:** a machine that is running. drop has to read the data to show it to you, so
anything with your session can ask drop.

**What still leaks even sealed:** directory names are peer ids and their sizes are visible — who you
talk to, and roughly how much. Files that arrived are left alone; somebody asked for them, in a
directory they chose. `peers.json` is in the clear, and whoever takes it can *be* you to every
device you have paired with.

A locked device — the key unplugged, the file gone — is a locked device, not an empty one. drop says
so rather than reporting a conversation that is there as missing.

## Keeping it small

A shared thing's record would otherwise grow by a copy per save. Once everybody still remembered has
seen all of it, the whole record is replaced by one change carrying what it came to. That is
[folding](shared.md), and it is what turns a demo into something you can leave running for a month.

## Where it lives

`src/pkg/keep/` writes files atomically and holds the lock. `src/pkg/book/`, `src/pkg/grant/`,
`src/pkg/made/`, `src/pkg/seen/`, `src/pkg/asked/` and `src/pkg/shares/` are the small stores.
`src/pkg/convo/` is conversations. `src/pkg/vault/` is the data key. `src/pkg/history/` is the
signed record.
