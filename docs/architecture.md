# How it is put together

drop is one binary and one daemon. The daemon holds an iroh endpoint, answers three protocols, and
serves a table of namespaces. Every other command is a thin client that either talks to the local
daemon or dials somebody else's directly.

```
                     drop serve
                         │
     ┌───────────────────┼───────────────────┐
     │                   │                   │
  drop/hello/1     drop/session/1       drop/pair/1
  what do you       open a path         become known
   serve me?                             to each other
     │                   │                   │
     └───────────────────┴───────────────────┘
                         │
              ns.Table ── the namespaces
                         │
              arch.Registry ── what each one means
```

## The layers

Each layer knows less than the one above it, and the boundaries are the point.

| layer | package | knows |
|---|---|---|
| transport | `node` | the endpoint key, relays, being findable |
| framing | `wire` | varints, lengths, frames — no meaning at all |
| protocol | `proto` | pairing, hello, opening a path, who is calling |
| the table | `ns` | paths, access rules, longest-prefix resolution |
| meaning | `arch` | the interface every archetype implements, and the registry |
| an archetype | `arch/chat`, `arch/files`, … | one kind of thing, and nothing about the others |

The rule that holds it together is in [namespaces](namespaces.md): a namespace knows which archetype
it belongs to and does not know what that archetype *means*. `ns`, `conf`, `proto` and `wire` have
no case for `chat` or `files` anywhere in them.

## What happens when somebody opens a path

```
  caller                                        this machine
  ──────                                        ────────────
  dial ALPN drop/session/1  ───────────────►
                                                QUIC proves which endpoint
  Open{path, archetype, badge, plate, …} ───►
                                                bounded read, 256 KiB       ← security.md
                                                handover applied, if any    ← identity.md
                                                badge → who is calling      ← pairing.md
                                                path cleaned, mount found   ← access.md
                                                rule asked
                            ◄───────────────    Accept   or   Reject{reason}
  ...whatever the archetype says...             the stream belongs to the archetype
```

Everything above the dotted line is generic and happens for every archetype. Below it, nothing
generic reads another byte: the archetype owns the stream. That is why an archetype can invent its
own protocol without any other package learning a case.

The order matters and is deliberate. The frame is size-bounded *before* it is allocated, because the
caller chose that number. The handover is applied before the caller is described, so a machine that
moved is recognised on the connection that carries the news rather than the next one. The rule is
asked last, because asking it can cost 64 MiB of argon2.

## One node, however many commands

Only one process can hold the endpoint. So `drop serve` holds it, and every other command asks the
daemon over a local socket rather than opening a second endpoint that would fight it for the port.

```
drop file ls orin:/read     ─┐
drop connect bob::/chat      ├── ask the running daemon to reach out
drop peer pair <ticket>     ─┘
```

If no daemon is running, the command dials for itself and stops when it is done. That is what makes
`drop file ls` work on a machine where nothing was started — and why a command that needs to *be*
reachable, like pairing by ticket, tells you to start one.


## Staying reachable

`drop serve` keeps the node reachable. As a user service, the device is reachable whenever it is on:

```console
install -m 0644 misc/drop.service ~/.config/systemd/user/
systemctl --user enable --now drop
```

Without a daemon, `drop connect laptop:/…` only reaches the laptop while somebody there is running
something that serves.

## When only one side can be reached

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

## Where things live

```
src/main.go            entry point; the version lives here
src/cmd/               the cobra command tree, one file per command
src/pkg/node/          identity, the iroh endpoint, relays
src/pkg/metal/         what machine this is, off the TPM or a serial
src/pkg/plate/         a machine vouching for the drops on it, and for what it became
src/pkg/wire/          the binary encoding: varints, no reflection
src/pkg/proto/         pairing, hello, opening a namespace, and the framing under them
src/pkg/ns/            namespaces: paths, the access rules on them, nothing about meaning
src/pkg/arch/          archetypes: the interface, the registry, and one package each
src/pkg/history/       what happened to one thing: signed changes, causally ordered
src/pkg/weave/         putting versions of one thing back together
src/pkg/plain/         text from somewhere else, made safe to put on a terminal
src/pkg/keep/          writing a file so a reader never sees half of one, one writer at a time
```

The full tree is in the README. What is worth noticing here is the direction of the arrows: `arch`
imports `ns`, never the other way round; `proto` imports neither `arch/chat` nor any other
archetype; and `plain` is imported by everything that prints.
