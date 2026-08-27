# Testing it

Three suites, and they answer different questions.

```console
make test         # the unit suite — 39 packages
make test-all     # the same, with the race detector
make cover        # the same, with a coverage profile
make e2e          # two real nodes on this machine, over QUIC
```

```console
go test -tags cross -count=1 -v ./src/e2e/   # this machine and another one
```

`make verify` is the whole local gate: `fmt-check`, `check`, `test`, `build`. It is what runs before
a commit.

## The unit suite

Fast, and run on every save. It covers the things that can be decided without a network: the merge,
the change ordering, path cleaning, access rules, the sanitiser, the wire codec.

Where a test exists because something was once wrong, it says so in its own words rather than in a
comment about a ticket. `TestARefusedMachineStaysRefusedWhenItNamesAPerson` is a sentence about what
must be true; that it also happens to be a regression test is secondary.

## The differential merge

The three-way merge is checked against the real thing rather than against expectations:

> **30,000 seeded random merges run against `git merge-file`**, failing on any silent divergence or
> spurious conflict.

That is worth more than any number of hand-written cases, because the failure mode being guarded
against is "quietly produces something plausible and wrong".

## Fuzzing

Twenty-two targets, beside the code they exercise, as `fuzz_test.go`. They cover everything a
stranger can put in front of the daemon: the opening, the hello, a badge, a stamp, a handover, a
change, a files request and reply, path cleaning, the terminal screen parser, the wire reader, and
the sanitiser itself.

```console
go test -run xxx -fuzz FuzzDecodeOpen -fuzztime 60s ./src/pkg/proto/
```

A panic from untrusted input is the whole daemon, not one namespace, so the targets assert more than
"it did not crash": nothing comes back bigger than the bytes it came from, nothing exceeds the bound
its own field was read with, and anything printed has been through the sanitiser.

Two of them found real bugs in code written the same day. The sanitiser appended its truncation mark
*past* the limit, so it produced output its own "is this safe" check rejected — and those two
functions disagreeing about safety matters, because signed data is held to one and unsigned goes
through the other. Path cleaning accepted a 512-character path, prepended a slash, and then exceeded
its own limit, so its output was not valid input to itself.

The corpus is committed under `testdata/fuzz/`.

## e2e

Two real nodes on this machine, driven from the command line over QUIC: pairing, a message each way,
a file each way, standard input as a file, a link, a stream, a shell, a cast, and a message queued
for a device that was switched off.

It is behind a build tag and not part of `make test`, because it builds the binary, starts daemons
and takes about three minutes. That belongs in a decision, not in every run of the unit tests.

## cross

The third suite needs a second machine, reachable over SSH and running the same build. It is what
proves a thing works between two **architectures** rather than between two processes that happen to
agree — the same binary compiled for amd64 and arm64, talking to each other.

```console
misc/orin.sh deploy      # build for arm64, copy it over, restart the daemon there
misc/orin.sh log 40      # what it said
misc/orin.sh me machine  # bare words run drop there
```

`$ORIN` is the host it uses. There is no `make` recipe for it, because it needs something `make`
cannot check for.

This is where the interesting failures show up. A wire format that changed on one side only reads as
`malformed unsigned varint` on the other — there is no compatibility shim anywhere in drop, by
design, so both ends must be the same build.

## Proving a test is worth having

A test that passes with and without the fix proves nothing. Where a fix is subtle — a lock, a bound,
a teardown — the way to check the test is to take the fix out and watch it fail:

| | |
|---|---|
| the address book lock | 20/20 runs fail without it |
| `paths.json` and grants | 10/10 each |
| the vault key | 10/10 |
| the stream teardown | a 30-second timeout without it, 0.05s with |
| the conversation length | `panic: slice bounds out of range` |
| the fold size limit | 33 MB written past a 33 MB cap |

## The build environment

The dev shell sets `CGO_ENABLED=0`, which the race detector cannot build under, so `make test-all`
sets it back. If you run the race detector by hand it needs `CGO_ENABLED=1`.

On a machine with a small `/tmp`, point `TMPDIR` somewhere with room: the e2e suite and fuzzing both
write a lot, and a full `/tmp` fails in ways that look like the code is broken.

## Where it lives

Unit tests beside what they test. `src/e2e/` holds both tagged suites — `e2e` and `cross`.
`misc/orin.sh` is the deploy script for the second machine.
