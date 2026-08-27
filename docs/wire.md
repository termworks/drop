# The wire

drop's own binary encoding: varints, no reflection, no JSON. A frame is a kind byte, a length, and a
body.

```
  ┌──────┬─────────────┬──────────────────────────┐
  │ kind │ length      │ body                     │
  │ 1 B  │ uvarint     │ length bytes             │
  └──────┴─────────────┴──────────────────────────┘
```

| | |
|---|---|
| `MaxFrame` | 4 MiB — what a transfer needs |
| `MaxString` | 64 KiB — any one string inside a frame |

Reading is bounded at every step, and where the bound matters most is before anybody has been
authenticated: see [hardening](security.md).

## Three protocols

ALPN is negotiated per connection in iroh, so each protocol is its own ALPN and a connection is one
kind of conversation.

| | |
|---|---|
| `drop/hello/1` | what do you serve me? — answered to anybody who dials |
| `drop/session/1` | open a path |
| `drop/pair/1` | become known to each other |

## The opening

Only the first two frames are everybody's.

```
  Open{path, archetype, version, badge, plate, handover}  ──────►
                                       ◄──────  Accept   or   Reject{reason}
```

Three separate claims ride in that frame, answering three different questions:

| | signed by | says |
|---|---|---|
| badge | a user key | whose machine this is |
| plate | a machine key | what machine the drop is running on |
| handover | the old machine's endpoint key | this caller is what that machine became |

Each is checked against the endpoint QUIC already proved. Any of them failing yields a caller less
is known about rather than a refused connection — an expired badge is not a device that vanished.

The opening is capped at 256 KiB, which is above the ~200 KiB one can legally be and far below
`MaxFrame`. That number is read from a stranger.

## Then whatever the archetype says

After `Accept` the stream belongs to the archetype and nothing generic reads another byte of it.
Four shapes are in use, and an archetype is free to invent a fifth.

**A push** — `share`:

```
sender                          receiver
  Item{names, sizes}   ──────►         # the whole offer, in one frame
        ◄──────  Accept{resume[]}      # per item, bytes already held
  Data … Data          ──────►         # then each item in turn
  End{size, digest}    ──────►
        ◄──────  Ack{ok}               # hashed and verified
```

**A batch** — `chat` and `link`: what is acknowledged is what reached a disk, nothing more.

```
  Item{message}        ──────►         # as many as there are
  End                  ──────►
        ◄──────  Ack{the ids stored}
```

**Rounds** — `files`: one request and one reply at a time, for as long as the caller keeps asking.

```
        ◄──────  Reply{writable}       # what this mount allows, said once
  Request{list /papers}   ──────►
        ◄──────  Reply{entries}
  Request{get thesis.pdf} ──────►
        ◄──────  Reply{size}, then Data … End, and an Ack back
```

**A duplex** — `tty` and `stream`: both ends writing at once, nobody counting.

```
  Data          ◄── both ways ──►  Data
  Resize        ◄── both ways ──►  Resize
  End (half-close)     ──────►
```

The two directions are independent. One end finishing does not end the other — the same way a pipe
closing its input does not stop it producing output. `Close` writes an `End` and half-closes the
stream, so the far end reads a real end of file while its own writes keep working.

## Sizes, known and unknown

An item can be offered without a size. A `stream` does not know how much its command will write, and
a file read from standard input does not know either. So `End` carries the size and the digest, and
what is acknowledged is what was actually hashed — not what was promised.

That is why a share is verified at the end rather than counted along the way, and why a `.part` file
records how much arrived rather than how much was expected.

## Meeting

A namespace several machines hold has a fourth conversation, carried inside `drop/session/1` with
`Meet` set on the opening. It is about a *thing* rather than about a path, named by an id both
machines compute — a path is one machine's own word for it, and two machines that spell it
differently would otherwise catch up about two different things.

```
  heads I have          ──────►
        ◄──────  what I am missing
  the changes you lack  ──────►
        ◄──────  taken
```

Details in [one thing, several machines](shared.md).

## Where it lives

`src/pkg/wire/` is the encoding and the framing, and knows nothing about meaning.
`src/pkg/proto/` is the three protocols and the opening. Each archetype's own shape is in its own
package under `src/pkg/arch/`.
