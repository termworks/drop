# Sharing a terminal

Two different things share a terminal, and they are not the same one.

A **tty namespace** hands the far end a shell on this machine. It is declared in the config, it
starts when somebody opens it, and `input` decides whether they may type into it.

```lua
drop.mount("/term", { type = "tty", access = { "laptop" }, shell = "/bin/sh", input = true })
```

A **cast** is the other direction: a terminal read from standard input and served as an asciicast,
for output that was recorded rather than a shell that is live.

```console
drop path cast bob:laptop:/watch < session.cast
```

## One shell, or one each

A tty namespace is one terminal, not one per watcher, unless it says otherwise. Everybody looking at
it sees the same thing and types into the same shell, which is the point — it is for sitting beside
somebody. The listing says so before anybody opens it: *one shell, shared: everybody on it sees what
you type*.

`private = true` is the other thing: a shell of your own, which nobody else sees and which ends when
you leave. It is what reaching your own machine from a phone wants.

```lua
drop.mount("/btop", { type = "tty", shell = "btop", access = "paired" })                  -- one, watched
drop.mount("/pair", { type = "tty", input = true, access = { "bob" } })                  -- one, shared
drop.mount("/me",   { type = "tty", input = true, private = true, access = { "me" } })   -- one each
```

When the shell exits, the terminal leaves the table so the next watcher starts a fresh one. Getting
that wrong is how one watcher could finish the namespace for everybody; see
[hardening](security.md).

## Joining one already running

What a watcher is handed on joining is the **screen**, not a replay of recent bytes.

A full-screen program draws by moving the cursor and changing the cells that altered, so a tail of
output holds whichever cells happened to change lately. Join `btop` halfway through and you get
those and nothing else: a screen with holes in it, filling in slowly as the program repaints.

So the serving side keeps a terminal of its own, feeds every byte through it, and hands a joiner the
picture as it stands. However long the program has been running, that is one screenful.

```
   shell ──► pty ──► a terminal drop keeps ──┬──► watcher, from the first byte
                     (cells, not bytes)      ├──► watcher, joining now: the screen
                                             └──► the scrollback
```

## How big it is

A shared terminal is **as big as the smallest window watching it**, so everybody sees all of it and
nobody a corner — the rule tmux uses. When the small window goes, it grows to the next smallest. Every
watcher is told its shape each time it changes, and draws it at exactly that shape: a terminal held
to somebody else's smaller window is drawn that size with the pane around it left empty, and one
bigger than your window is cut at the edge rather than wrapped.

It is never held below 80×24 while more than one is watching. One small window would otherwise take
everybody else's with it — a phone held upright shrinking a program others are using to a size it
refuses to draw at. The small window is the one that crops, or on a phone, the one that scales down.
Watched by one, the terminal is that window's, whatever size it is.

What the terminal is sits above it:

```
/term · live · shared, 2 watching · 100×30, held to a smaller window
/mine · typing · your own shell · 146×35
/btop · watching · shared, only you · 146×35
```

From a plain terminal, `drop connect` puts the same in the window's title, which is the one place a
raw terminal has for it that does not draw over the far end's screen. A watcher that goes without
saying so — a window closed, a laptop shut — is noticed within fifteen seconds by the pings it stops
answering, and stops holding the terminal to its size.

## Watching, and typing

A terminal takes its shape from whoever is looking at it, **whether or not they may type into it**:
a pty drawing for a window nobody has wraps every line in the wrong place. Shape is presentation,
not input, and it is sent either way.

What is typed is the part `input` decides, and the *far end* decides it — anything sent to a
read-only terminal is dropped there rather than refused here. That way a watcher's keystrokes never
depend on their own client behaving.

In the interface, `i` gives a terminal the keyboard and **ctrl+]** takes it back. While it has the
keyboard it gets every key there is, `esc` and `q` included, because half a keyboard is not a
terminal.

## What it costs

The screen parser is fuzzed, because it is fed bytes from a program that can write anything and an
escape sequence that made it allocate without bound would be a way in. See
[testing](testing.md).

A command a person backgrounds in a shared shell keeps running after they leave. That is what a
terminal is, and drop does not try to be cleverer than the shell about it — but the *session* ends
regardless, so the namespace is free for the next watcher.

## Where it lives

`src/pkg/arch/tty/` is the namespace and the shell. `src/pkg/term/` is the terminal drop keeps —
cells rebuilt from what a device sends. `src/pkg/cast/` fans one terminal out to many watchers, and
`src/pkg/asciicast/` reads a recorded stream.
