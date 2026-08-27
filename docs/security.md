# Hardening

What is bounded, what is refused, and what is not defended. Most of this document exists because
something in it was once possible; where that is so, it says what the attack was.

## Before anybody has been let in

Two frames are read from a caller nobody has decided anything about yet: the opening, and the ask
that starts a hello. Everything either can lead to is work a stranger chose, so all of it is bounded
before it is done.

**How much memory a frame may claim.** The size of a frame is a number the sender writes. The
general limit is what a *transfer* needs — 4 MiB — and that is twenty times what either pre-auth
frame can legally be. Read before authentication, that number is a stranger naming how many
megabytes to set aside for them, at five bytes each, held until the deadline expires. Measured
before it was fixed: **200 stalled streams, fed 1000 bytes in total, grew the heap by 812 MiB.**
Both pre-auth reads now refuse at the header, at 256 KiB, before anything is allocated.

**How long a read may wait.** Every read in the handshake has a deadline, on both sides. A far end
that takes what you sent and then says nothing would otherwise hold a goroutine, a stream and a
buffer for as long as it liked.

**How much a guess may cost.** A password-guarded path costs 64 MiB and three passes of argon2 to
try, which is the point of it. A caller gets six tries a minute. Two things make that number mean
what it says:

- Getting in somewhere that asked for no password does not forgive a guess. It used to, so a peer
  with two streams could alternate — guess, open something public, guess again — for ever.
- One guess is hashed once however many rules ask about it. Resolving a path walks every rule above
  it; without a cache, **one guess against eight rules cost eight hashes**. It now costs one.

**How much disk a stranger may spend.** A device nobody knows that dials is written down so you can
let it in later without copying its id out of a log. That write is flushed to the disk itself, so it
happens at most once every thirty seconds per device rather than once per dial.

## What a peer says, and what your terminal does with it

A terminal is not a display, it is an interpreter. Everything a peer sends that drop then prints —
what it calls itself, what it says a namespace is for, the name on a badge, a file in a listing, a
message, the reason a stranger gives for wanting in — is bytes somebody else chose, going to a
program that obeys them.

```
  /private             files    read-only
  /chat                chat     a chat␛[1A␛[2K  /secrets   files   read and write
                                        ▲
                        moves up one line and erases it, then writes its own
```

The listing looks perfectly ordinary afterwards. That was possible.

Nothing off the wire reaches a terminal as it arrived. What survives is one line of printable
characters, of bounded length, in the order it will be read: no escapes, no C0 or C1 controls,
nothing that reorders what follows it (bidi overrides — the "trojan source" trick), nothing
zero-width, and nothing long enough to push the rest of a listing off the screen.

Where that happens depends on whether anybody signed it:

| | |
|---|---|
| **unsigned** — a hello, a listing, a directory | cleaned where it arrives, so a place that prints it later cannot forget |
| **signed** — a badge, a plate, a handover | **refused**, because cleaning would mean the bytes checked and the bytes shown are two different strings with a signature over only one |
| **compared as well as shown** — a remote file name, a holder key | cleaned only at the point of display, because cleaning at decode would change a value used in logic |

A conversation is kept as it was said. It is a record, and a record that quietly differs from what
arrived is not one. The cleaning happens where it stops being a record and becomes output — verified
across two machines: a message carrying an escape has **one ESC byte stored on disk and none in what
is printed**.

Names *you* wrote — what you called a machine in your own address book — are yours and are printed
as you typed them. Nothing from the network is ever one of those.

## What a peer can do to a shared thing

A namespace several machines hold takes signed changes from anybody the rule admits. Three things
are guarded:

**It cannot make the namespace unreadable.** The history is read back by ordering it, and ordering
refuses one whose changes name each other in a circle. A circle that got written down would make the
namespace unreadable on every machine that took the change, after every restart, for good. A fold is
the one thing that changes what a change is placed behind *after* it has been stored, so a fold must
cover every head it names; one that does not is refused.

**It cannot fill the disk.** A fold is exempt from the size limits because it is what makes a full
log smaller. That exemption belongs only to a fold standing for *everything* held. Without that
distinction, **32 changes calling themselves folds wrote 33 MB past a 33 MB cap.**

**It cannot choose what lands.** A file too big to travel inside a change is fetched from whoever
has it, and the digest inside the signed change is the one account of that version that the sender
could not have made up. Bytes that do not match are dropped and the round tries somebody else.
Without that, whoever answered chose the content, on a path of the change's choosing, with the mode
the change asked for applied afterwards.

## Sessions end

A `stream` namespace runs a command; a `tty` namespace runs a shell. Both used to be able to outlive
the session that started them.

`stream` gives its command a process group and kills the group, and closes the read side when the
session ends. Before that, a command that left anything behind — a pipeline, a backgrounded job —
left it holding the output pipe, and the copy waited on a read that never finished. Not when the
peer hung up, not when the session ended, not at daemon shutdown.

`tty` is subtler and the fix is different. A shell with a terminal turns job control **on**, so
anything a person backgrounds gets a process group of its own — a group kill cannot reach it. So
what ends a tty session is the shell being waited for, and the tidying happens there rather than
behind the pty read. Before that, one watcher who typed `sleep 600 &` and exited left the terminal
in the table with no shell behind it, and **every later watcher of that path got nothing until the
daemon restarted**.

## One writer at a time

The address book, the grants, the namespaces put up from the command line, the machine key and the
vault key are each one file shared by every drop on the machine — the daemon, the interface, and
each `drop peer pair` or `drop path create`. Read, change, write is three steps, and a second writer
landing between the first and the third has its change thrown away by the third.

Each of those changes now takes the file to itself first. The tests are checked by removing the
lock: **20/20, 10/10 and 10/10 runs fail without it.**

Two of them lose more than a pairing. Two processes each minting a vault key leaves whichever wrote
second owning the file while the other seals records with a key that is nowhere — and a record
sealed to a key that was never written down is not recoverable by anybody, ever.

## Bad bytes are not worth a panic

A panic in the daemon is every namespace gone, not one. Parsers are fuzzed: the opening, the hello,
the badge, the stamp, the handover, a change, a files request and reply, path cleaning, and the
sanitiser itself. Roughly 100 million executions, and the corpus is committed.

Two of those found real bugs, both in code written the same day: the sanitiser appended its
truncation mark *past* the limit, so it produced output its own "is this safe" check rejected; and
path cleaning accepted a 512-character path, prepended a slash, and then exceeded its own limit — so
its output was not valid input to itself.

A length prefix in a conversation log could also wrap negative past a signed bounds check and panic
the reader on a corrupt file.

## What is not defended

**A machine that is running.** drop has to read your data to show it to you, so anything with your
session can ask drop. No design at this level changes that.

**Another account on your machine, against the machine key.** Where there is no TPM, the serial the
machine key is derived from is readable by every account. That is [written down](identity.md) and
deliberate: machine identity locates hardware, and no access rule is satisfied by it.

**Somebody who has your user key.** It is your identity. A stolen one is you until the badge expires
or the far end forgets you.

**Traffic analysis by a relay.** A relay knows two parties are talking, though not who they are or
what they say. See [pairing](pairing.md) for what the rendezvous does and does not hide.

**Background processes a person starts in their own shell.** A `tty` watcher who backgrounds
something and leaves keeps it running. That is what a terminal is.

## Where it lives

`src/pkg/plain/` is the sanitiser. `src/pkg/keep/` is atomic writes and the advisory lock.
`src/pkg/proto/session.go` and `hello.go` carry the pre-auth bounds; `src/pkg/proto/guess.go` the
guess allowance. `src/pkg/history/log.go` guards the change graph. Fuzz targets sit beside the code
they exercise, as `fuzz_test.go`.
