# Documentation

One document per subject: what it is, how it works, what it cannot do, and where it lives in the
tree. Each carries the settings and the limits that belong to it, so there is one page per subject
rather than a shallow one and a deep one.

Every claim in here was checked against the source or against a running build, on two machines of
different architectures. Where a design was forced by a measurement, a bug or an attack, the
document says so, because those are the sentences worth reading twice.

## The shape of the thing

| | |
|---|---|
| [How it is put together](architecture.md) | the layers, one daemon, and what happens when somebody opens a path |
| [What names a machine](identity.md) | identity taken from the hardware, several people on one machine, moving to another |
| [Namespaces and archetypes](namespaces.md) | an instance, a meaning, and the rule that keeps them apart |
| [The wire](wire.md) | frames, the opening, and the shape each archetype speaks afterwards |

## Using it

| | |
|---|---|
| [The command line](cli.md) | one noun per group, and an address that reads right to left |
| [The archetypes](archetypes.md) | share, files, chat, note, link, stream, tty — what each is for |
| [One thing, several machines](shared.md) | signed changes, merging, and how a history stays small |
| [An archetype in Lua](lua.md) | a plugin both ends load, and the sandbox it runs in |

## Who may reach what

| | |
|---|---|
| [Access rules](access.md) | the vocabulary, how it inherits, and what a refusal means |
| [Pairing](pairing.md) | recognition against trust, badges, and being found without being findable |

## What is kept, and what is defended

| | |
|---|---|
| [On the disk](storage.md) | where everything lives, the vault, and one writer at a time |
| [Hardening](security.md) | what is bounded before anybody is let in, and what is not defended |

## Running it

| | |
|---|---|
| [Configuration](config.md) | `init.lua`, and the settings that are not namespaces |
| [Testing it](testing.md) | three suites, fuzzing, and two real machines |
