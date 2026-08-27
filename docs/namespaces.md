# Namespaces and archetypes

A **namespace** is an instance: a path this node serves, a rule about who may reach it, and a bag of
settings. An **archetype** is what that instance *is* — the operations it answers, the settings it
reads, the state it keeps.

```
  /inbox     is a namespace   ─── belongs to ───►  share    is an archetype
  /papers    is a namespace   ─── belongs to ───►  files
  /notes     is a namespace   ─── belongs to ───►  note
  /standup   is a namespace   ─── belongs to ───►  note      ← two instances of one thing
```

Two namespaces of the same archetype are two instances of one thing, the way `friends` and `work`
are both chats.

## The rule

> A namespace knows which archetype it belongs to, and does not know what that archetype means.

Adding an archetype is writing an implementation and registering it. Nothing in the namespace layer,
the config reader, the protocol or the wire gains a case. If adding one requires modifying the
namespace implementation, the abstraction is leaking.

What that buys, concretely: `ns`, `conf`, `proto` and `wire` contain no reference to `chat`,
`files`, `note` or any other archetype. Grep for it. The config reader hands an archetype a bag of
names it does not understand; the archetype reads its own settings out of it and refuses a
declaration it cannot serve, once, when the config is read — so a mistake is reported with a file
and a line rather than as silence months later.

## The interface

```go
type Archetype interface {
    Name() string          // what a config writes and what travels on the wire
    Version() int          // which revision of this archetype's own protocol that name means

    Read(Declared) (Config, error)   // the settings, or a refusal
    Note(Config) Note                // what may be said about one, without knowing what it is
    Serve(ctx, Session) error        // answer one session
}
```

`Config` is `any`. The namespace holds it and hands it back and never looks inside. `Declared` is
the declaration as the config wrote it, before anybody decided what the words mean — each accessor
says whether the setting was mentioned at all, so an archetype can tell "off" from "unset".

`Note` is everything that can be said about a namespace *without* knowing what it is, so a listing,
a row in the interface and the startup table can describe one of any archetype — including one
written next week — without a case for each.

| field | what it says |
|---|---|
| `Writable` | the far end may put something into it |
| `Shareable` | several machines may hold one of these and see each other's changes |
| `Detail` | this instance in one column: where it points, what it runs |
| `About` | what this archetype is for, in the words of somebody explaining it once |
| `Glyph` | one character, for a list with no room for a word |
| `Shape` | another archetype whose protocol this one speaks |

`Shape` is the fallback for a machine that has never heard of an archetype: a `camera` whose shape
is `chat` is opened with the chat opener and drawn with the chat glyph, because the far end only
ever needed to know what to say down the stream. An archetype that names none is one nobody else can
open, which is honest and is sometimes the point.

There is one optional interface. An archetype that can say a namespace needs attention implements
`Amiss(Config) string`, and is asked for it by interface rather than by name. It is handed the
settings and not a path, because whoever asks is often not the process keeping the namespace —
`drop path ls` is its own process, and what it can see is what is on the disk.

## The registry

```go
known := arch.NewRegistry()
known.Register(chat.New(...))
known.Register(files.New(...))
```

A value rather than a package variable, because which archetypes a process serves is a property of
that process: `drop serve` registers everything, a chat window registers one, and a test registers a
fake. Versions live beside names — `Lookup(name, 0)` means whichever is newest, which is what a
config that names no version is asking for.

A name nobody registered is refused with the list of what *is* registered, which is the difference
between a typo you can see and a namespace that silently serves nothing.

## Longest prefix wins

```
/            access = paired
/work        type = files, dir = ~/work
/work/secret access = { "me" }
```

Resolving a path walks up until it finds a mount. A rule written deeper **replaces** the one above
rather than adding to it, so `/work/secret` is reachable by `me` and by nobody else, whatever `/`
said. A path with no rule above it is reachable by nobody: the default is nothing, and a config that
declares no rule at the root serves no one.

A branch is a path that holds others and serves nothing itself — no type, only a rule. That is what
`drop.mount("/", { access = "paired" })` is.

Mounts are keyed by path, so declaring one twice replaces it rather than adding a second: a config
that loops, or is read again, cannot silently grow the table.

## Adding one

Six methods and a registration. The test that proves the abstraction holds defines a whole
`camera` archetype inside a single test file — settings, wire shape, glyph — and registers it into a
fresh registry, without touching a line of `ns`, `conf`, `proto` or `wire`. If that test ever needs
to change one of those packages, the rule above has been broken.

For an archetype that both ends must agree on without either recompiling, see
[an archetype in Lua](lua.md).

## Where it lives

`src/pkg/arch/arch.go` is the interface and the registry. `src/pkg/ns/` is paths and rules and knows
nothing else. Each archetype is a package under `src/pkg/arch/`.
