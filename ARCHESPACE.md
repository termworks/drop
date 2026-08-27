# Namespace and Archetype Architecture

## Core Idea

Keep **namespaces** and **archetypes** strictly separated.

```text
Namespace = an instance
Archetype = what that instance means and how it behaves
```

For example:

```text
Namespace: friends
Archetype: chat

Namespace: shell
Archetype: terminal

Namespace: home
Archetype: filesystem
```

Multiple namespaces can use the same archetype:

```text
                 ┌── "friends" ──────┐
                 │                   │
                 ├── "work" ─────────┼──> Chat Archetype
                 │                   │
                 └── "project-x" ────┘
```

The namespace system should not understand chat, terminals, filesystems, or any other specific application.

---

## Namespace

A namespace should be a generic container pointing to an archetype.

Conceptually:

```text
Namespace
    id
    name
    archetype
    archetype_version
    archetype_config
```

For example:

```text
Namespace {
    id: "abc123"
    name: "shell"
    archetype: "terminal"
    archetype_version: 1
}
```

Another namespace:

```text
Namespace {
    id: "def456"
    name: "friends"
    archetype: "chat"
    archetype_version: 1
}
```

The namespace implementation should **not** contain application-specific concepts.

Avoid things such as:

```text
Namespace {
    messages
    files
    terminal_rows
    terminal_columns
    directories
}
```

The moment those concepts enter the namespace layer, the namespace abstraction stops being agnostic.

---

## Archetype

An archetype defines the semantics of a namespace.

Conceptually:

```text
Archetype
    id
    version
    operations
    configuration
    state
```

For example:

```text
chat
    version: 1

    operations:
        send
        history
```

```text
terminal
    version: 1

    operations:
        input
        output
        resize
        signal
```

```text
filesystem
    version: 1

    operations:
        list
        stat
        read
        write
        remove
```

The archetype implementation understands these operations.

The namespace does not.

---

## Relationship

The relationship should therefore be:

```text
Namespace
    │
    │ archetype_id
    ▼
Archetype
    │
    ├── operations
    ├── configuration
    └── implementation
```

A namespace is effectively an **instance of an archetype**.

This is similar conceptually to:

```text
Class       → Object
Archetype   → Namespace
```

Not literally in implementation, but it is a useful mental model.

`chat` defines what a chat namespace is.

`friends` is one particular instance of `chat`.

---

## Generic Dispatch

The namespace layer should only need a generic mechanism for passing something to its archetype.

Conceptually:

```text
namespace
    ↓
archetype
    ↓
operation
    ↓
payload
```

Something like:

```text
handle(namespace, operation, payload)
```

For example:

```text
handle(
    namespace = "friends",
    operation = "send",
    payload = ...
)
```

The namespace layer resolves:

```text
friends
    ↓
chat
```

and gives the operation to the chat archetype.

For another namespace:

```text
handle(
    namespace = "shell",
    operation = "resize",
    payload = ...
)
```

Resolution becomes:

```text
shell
    ↓
terminal
```

The generic namespace machinery does not need:

```text
handle_chat()
handle_terminal()
handle_filesystem()
```

Those distinctions should exist only after dispatching into the archetype.

---

## Archetype Interface

Every archetype should implement the same minimal outer interface.

Conceptually:

```text
Archetype
    create()
    open()
    handle()
    close()
```

The payload passed to `handle()` belongs entirely to that archetype.

For example:

```text
Chat.handle(...)
Terminal.handle(...)
Filesystem.handle(...)
```

The namespace layer sees all three simply as:

```text
Archetype.handle(...)
```

This is the important abstraction boundary.

---

## Archetype-Specific State

Any state that only makes sense for one archetype belongs to that archetype.

For example:

```text
Namespace
    id
    name
    archetype = terminal

Terminal State
    rows
    columns
    environment
```

or:

```text
Namespace
    id
    name
    archetype = chat

Chat State
    messages
    participants
    history
```

The namespace itself should not care about the structure of this state.

Conceptually it can simply hold:

```text
Namespace
    id
    name
    archetype
    state
```

where `state` is opaque from the namespace system's perspective.

The archetype owns its interpretation.

---

## Archetype Configuration

The same applies to configuration.

For example:

```text
terminal:
    shell = "/bin/zsh"
    scrollback = 10000
```

or:

```text
filesystem:
    root = "/home/user/shared"
    readonly = false
```

The namespace system should not interpret those fields.

It only associates configuration with the namespace:

```text
Namespace
    archetype = filesystem
    config = <opaque archetype configuration>
```

The filesystem archetype interprets it.

---

## Archetype Versions

Archetypes should be versioned independently.

For example:

```text
chat/1
terminal/1
filesystem/1
```

Later:

```text
chat/2
```

does not require changing the namespace abstraction.

A namespace simply references:

```text
archetype = chat
version = 2
```

This becomes especially useful if archetypes evolve significantly over time.

---

## Archetypes Can Have Different Internal Models

Do not force every archetype into the same internal model.

A chat archetype might internally operate on messages.

```text
Chat
    Message
    Message
    Message
```

A terminal might operate as continuous state and data.

```text
Terminal
    input
    output
    resize
```

A filesystem might expose a hierarchy.

```text
Filesystem
    /
    ├── foo
    └── bar
```

These are fundamentally different things.

That is fine.

The abstraction should exist **around** them, not inside them.

```text
             Namespace
                 │
                 ▼
             Archetype
                 │
       ┌─────────┼─────────┐
       ▼         ▼         ▼

     Chat     Terminal   Filesystem

   messages   streams      tree
```

Do not try to invent one universal data representation that makes all three internally identical.

---

## Adding a New Archetype

The goal should be that adding:

```text
camera
```

requires implementing:

```text
CameraArchetype
```

and registering:

```text
camera/1 → CameraArchetype
```

Nothing in the namespace implementation should change.

Likewise:

```text
git
database
clipboard
remote-desktop
audio
telemetry
```

should simply become additional archetypes.

If adding a new archetype requires modifying the namespace implementation, the abstraction is leaking.

---

## Registry

A simple archetype registry can connect identifiers to implementations.

Conceptually:

```text
Archetype Registry

chat/1        → Chat
terminal/1    → Terminal
filesystem/1  → Filesystem
```

Then:

```text
Namespace
    archetype = "terminal"
    version = 1
```

becomes:

```text
namespace
    ↓
"terminal/1"
    ↓
registry
    ↓
Terminal implementation
```

This keeps the namespace completely generic.

---

## Design Rule

The strongest rule for the architecture should be:

> **A namespace knows which archetype it belongs to, but it does not know what that archetype means.**

The archetype owns:

```text
behavior
operations
state
configuration
semantics
```

The namespace owns:

```text
identity
name
archetype reference
lifecycle
```

Therefore:

```text
Namespace
    │
    └── "I am an instance of archetype X"

Archetype
    │
    └── "I define what X actually means"
```

That boundary allows the archetype system to grow indefinitely without turning the namespace layer into a collection of special cases.

