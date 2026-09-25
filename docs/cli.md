# The command line

Three things, and the same three words everywhere: **people**, the **machines** each of them has,
and the **topics** on each machine — a chat, a folder, an inbox, a note, a terminal. Each is added,
listed, renamed and removed the same way.

```console
drop person add                show your code, for somebody to take
drop machine add               show a code a new machine of yours takes
drop topic add work folder     put a folder called work on this machine
```

`drop` with nothing after it opens the full-screen interface, which is the same three levels.

## An address

Whose machine, which machine, and what on it. The three parts read **from the right**, so leaving
one out leaves out the one on the left.

```
bob:laptop:/chat        bob's laptop, its /chat
laptop:/chat            the machine called laptop
bob::/chat              bob — whichever machine of his answers
/chat                   this machine
```

The empty middle in `bob::/chat` is not a typo: it says "a machine of bob's, any of them". That
works because pairing is with a person, so a machine of theirs you have never met still arrives with
a name. See [pairing](pairing.md).

## The groups

**`drop person`** — you, and everybody you added.

```console
drop person                    everybody, each with their machines; you first
drop person add                show your code and a QR for them to take
drop person add <code>         take the code they show
drop person add <name>         ask a device on this network; its person says yes
drop person trust <name>       the second, deliberate step: topics open to trusted open to them
drop person rename <n> <new>   call somebody something else, on every machine of yours
drop person rm <name>          remove them and every machine of theirs, everywhere
```

**`drop machine`** — your own machines.

```console
drop machine                   every machine of yours this one knows
drop machine add               show a code; only a machine holding your key can
drop machine add <code>        on the new machine: take it, and become yours
drop machine add <name>        ask a device you know, or one on this network, to become yours
drop machine add --key         hand the new machine your key, rather than a badge
drop machine rename <n> <new>  call one of them something else
drop machine rm <name>         take one out of yours, on every one of them
drop machine renew             sign fresh badges for those running low, with a key in hardware
```

The code screen says which key signs the new machine, and the new machine says which key it now
belongs to. A machine that only wears a badge cannot add another: `drop machine add` there says so,
and points at a machine that holds the key.

**`drop topic`** — what a machine offers.

```console
drop topic                     the topics on this machine
drop topic ls <machine>        the topics on another one, yours or somebody's
drop topic add <name> <kind>   add one here; --on <machine> adds it to another of yours
drop topic rm <name>           take one away (--on works here too)
drop topic who <name> [step]   who may open it: me, trusted, paired or anyone
drop topic kinds               chat, inbox, folder, note, terminal, links, stream
```

A folder, inbox or note lives under `~/drop` on the machine it is added to, so adding one from a
phone never needs the laptop's paths. A stream takes `--command`. A new topic opens only for you
until `drop topic who` says otherwise.

**`drop me key`** — who you are.

```console
drop me key                    your key, and every other key on this machine you could be
drop me key use <file> --yes   be the SSH key at a file, or the YubiKey whose .pub it is
drop me key yubikey            be the key in your YubiKey: its handles fetched, a PIN and a touch
drop me key yubikey --new      make a key for drop on the YubiKey first
```

drop makes a key of its own on first run, so it works with nothing set up. Choose your own and
every machine of yours that holds the same one — the same SSH key, or the same YubiKey — finds the
others by itself, with no code. A machine without it is added with `drop machine add`.

**`drop nearby`** — devices on this network nobody here has added yet.

```console
drop nearby                    devices on this network, and devices asking this one
drop nearby pair <name>        add one without a code; its person says yes
drop nearby mine <name>        make one of them yours, the same way
drop nearby yes <name>         say yes to one asking this machine, or no
```

Asking is answered on the other device, with the same number on both screens.

**`drop me`** — this machine, and who it belongs to.

```console
drop me id                     this machine's identity
drop me machine                what names it, and what would change it
drop me machine rebind         stop using a written-down key, be named by the hardware
drop me machine migrate <id>   say this machine became another one
drop me machine took <line>    on the new machine: take that statement up
drop me vault                  whether what is kept on this disk is encrypted
drop me leave                  take this machine back out of your machines
drop me reset --yes            delete everything drop knows here, and start over
drop me passwd                 hash a password, to guard a path with
drop me log [name]             a conversation, or all of them
```

The older spellings — `drop add`, `drop promote`, `drop join`, `drop peer …`, `drop path …`,
`drop me user …` — still answer, and are left out of the help.

**`drop file`** — what is inside a `files` namespace somebody shares.

```console
drop file ls <address>         list a directory
drop file get <address> [into] copy something out
drop file put <address> <src…> copy something in
drop file mkdir <address>      make a directory
drop file mv <address> <to>    move something
drop file rm <address>         remove something
```

**`drop connect`** — open whatever is at an address, whatever it turns out to be.

```console
drop connect bob:laptop:/chat  a chat window
drop connect orin:/term        a terminal
drop connect tron:/logs        a stream
```

It asks what is there and picks the right client. That is what `Shape` in
[namespaces](namespaces.md) is for: an archetype this build has never heard of still opens, as
whatever it says it speaks like.

**`drop tui`** — an interface open in another terminal, looked at and typed into from this one.

```console
drop tui ls                    the interfaces open on this machine
drop tui show [--to x]         print what one is showing
drop tui keys [--to x] <key>…  press keys: enter, esc, down, tab, ctrl+], or one character
drop tui type [--to x] <text>  type text into it, a key at a time
```

With more than one open, `--to` picks one by process id, device name, profile or id. It works over
ssh as well as it does here, so one person can walk another through both ends of a pairing while
both watch their own screens.

## Putting a namespace up without editing the config

```console
drop path create /notes files --set dir=~/notes --flag writable --access paired
drop path create /log stream --set command="journalctl -f" --access bob --keep
```

Without `--keep` the path is up for as long as the command runs and goes when you stop it. With
`--keep` it is written down as well and is there after a restart.

A setting is text (`--set`), on or off (`--flag`), or a list (`--list`), each with a flag of its own
— because a single one that guessed could not say that a piece of text is the word "true".

With no arguments, `drop path create` lists the types this build answers to.

## Where it lives

`src/cmd/`, one file per command. `src/cmd/address.go` parses an address; `src/cmd/root.go` builds
the tree.
