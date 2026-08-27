# The command line

One noun per group. `me` is this machine, `peer` is the machines it knows, `path` is what it serves
and who may reach it, `file` is what is inside a directory somebody shares. `connect` opens whatever
is at an address, and `serve` stays up.

```console
drop                    a full-screen interface: pick a device, then a path
drop path ls beta       the same thing from the command line
```

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

**`drop me`** — this machine, and who it belongs to.

```console
drop me id                     this machine's identity
drop me machine                what names it, and what would change it
drop me machine rebind         stop using a written-down key, be named by the hardware
drop me machine migrate <id>   say this machine became another one
drop me machine took <line>    on the new machine: take that statement up
drop me user                   who this machine belongs to
drop me vault                  whether what is kept on this disk is encrypted
drop me passwd                 hash a password, to guard a path with
drop me log [name]             a conversation, or all of them
```

**`drop peer`** — the machines this one knows.

```console
drop peer pair                 print a ticket
drop peer pair <ticket>        take one
drop peer ls                   everything in the address book
drop peer whois <name>         what this machine knows about another
drop peer trust <name>         the second, deliberate step after pairing
drop peer forget <name>        stop recognising it, immediately, and tell nobody
```

**`drop path`** — what this machine serves, and who may reach it.

```console
drop path ls [address]         what a machine serves — this one, or somebody else
drop path create <path> <type> put a namespace up
drop path rm <path>            take one off
drop path join <address>       hold a namespace somebody else holds
drop path grant <path> <who>   let somebody reach it
drop path revoke <path> <who>  stop them
drop path ask <address>        ask to be let into a path you can see and cannot open
drop path requests             who has asked
drop path share <address>      take a file from somebody, once
drop path cast <address>       serve a terminal read from stdin as asciicast
```

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
