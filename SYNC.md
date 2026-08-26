# Keeping a directory in step

`files` is a directory you reach into. This is a directory that keeps itself the same on two
machines. It is a different thing, and the analysis says plainly that it has to be a different
archetype rather than more operations on `files`.

## What was measured

Numbers from this machine and the Orin, not from reasoning.

| | |
| --- | --- |
| walk + stat a 5,526-entry, 2.18 GB folder | **14 ms** warm, 520 ms cold |
| hash the same folder | **3.8 s** — a 1000× gap, and the whole design rests on it |
| blake3 on one core | 6,650 MB/s, 2.7× sha256 |
| index of 10k files, prefix-shared, gzipped | ~410-615 KB, of which 320 KB is incompressible digest |
| `keep.Replace` of 512 KB on real ext4 | **18.7 ms** — and 79 µs on tmpfs, so a test on `/tmp` proves nothing |
| one append + fsync | 6.9 ms, so ~145/s: you cannot fsync per changed file |
| rolling checksum, Go, with lookups | 447 MB/s |

## Five things that are broken for sync today

Each was reproduced, not inferred.

1. **`Entry.At` is whole seconds.** The filesystem gives nanoseconds. Two writes 3 ms apart are
   identical on the wire — 500/500 rapid rewrites invisible at second resolution, 0/500 at
   nanosecond. This is the most likely way sync ships broken and looks fine in testing.
2. **`files` put cannot replace.** `place` → `claim` numbers around a collision, so a put over
   `notes.txt` produces `notes-1.txt`, `notes-2.txt`. There is a test asserting this. A
   synchroniser's whole job is replacing a file with a newer one.
3. **Nothing calls `Chtimes` anywhere in the repo.** An arriving file's mtime is the moment it
   landed, so it looks locally modified on the next scan and wants to go back. That presents as
   "sync never converges", not as a missing call.
4. **Mode is flattened to 0600/0700**, measured 755 → 700. If mode is compared, two machines flap
   forever and neither is wrong.
5. **`files` has no resume.** `partName` uses `crypto/rand`, and a hard kill mid-get leaves an
   orphan part — 87 MB per attempt, two orphans from two runs. Only `share` resumes, and it keys
   on (name, size), which resumes against stale bytes and then throws the transfer away.

Also found and worth fixing regardless of sync: the announced size is never enforced against what
arrives (8388608 announced, 1835008 landed, no error), and a directory of 16385 entries cannot be
listed at all.

## The shape

A new archetype, `sync`, at version 1. It reuses the wire, the containment, the transfer handshake
and the path cleaner, and owns everything else — which is what ARCHESPACE says an archetype is for.

**Remember pairwise, not globally.** Syncthing's version vectors buy N-way sync with no pairwise
state, and pay for it with tombstones that cannot be collected exactly — kept forever until 2.0,
then six months, with documented file resurrection as the price, and indexes that reach 100 GB in
the field. For a handful of machines one person owns, Unison's model is right: remember the last
agreed state per pair. Tombstone collection becomes exact, because you know every peer.

**Detect change by digest against last-synced digest, on both sides. Never by comparing clocks.**
drop has no clock authority, so "older mtime loses" is not available to it. Two sides differing
from the last agreed state is a conflict by definition, and no timestamp makes it not one.

**Never guess at a conflict.** Every product answer — Dropbox, Nextcloud, Syncthing — keeps both
and tells the person. Nobody merges. `claim` already produces the numbered name for the loser.

**Content-equality is never a conflict.** That single rule handles both the change-arriving-by-two-
routes case and the infinite-resync loop.

**A rename is a delete plus a create, with a digest lookup before transferring.** That recovers
renames, copies and moves for one map lookup and no bytes on the wire.

**If the state file for a peer is missing, sync additively and delete nothing.** This is the rule
that turns a catastrophe into clutter.

## Order of work

1. **The scanner, alone, with no networking.** Walk through `os.Root.FS()` — measured 1.0-1.4×
   a plain walk, where a per-name `Root.Lstat` is 2.6-3.6×. Record path, size, mtime to the
   nanosecond, inode, mode, and blake3 of the content.
2. **The index**, at `DataDir()/sync/<namespace>/`, beside conversations. Not the config dir, and
   not a cache: a tombstone recording that a file was *deleted* rather than never present cannot be
   recomputed from the filesystem. Lose the index and every deletion is resurrected.
3. **The digest cache**, with git's racy rule built in from the start: skip the hash when
   (size, mtime_s, mtime_ns, inode) match, and force a re-hash for anything whose mtime is not
   strictly older than the index's own write. Because `Chtimes`-restore defeats every tuple — a
   restore-from-backup produces a byte-identical signature over different content — add a deep
   re-hash on a slow timer, and make its cost visible.
4. **The comparison.** Sequence numbers per Syncthing's model, not a Merkle tree: measured, a
   Merkle descent was *worse* than shipping the whole index at ~10 changed files in a 4,652-file
   folder, because one mismatched directory puts its whole child list on the wire and each level
   is a serial round trip.
5. **The transfer**, reusing `sendBody`/`drain` unchanged, plus a replace with a precondition —
   "replace this path, whose content I believe is digest D" — so a concurrent change is detected
   rather than clobbered. `opMove` already overwrites, so put-to-temp-then-move gives atomic
   replace with no new operation.
6. **Metadata policy, written down.** Set mtime on arrival. Preserve only the execute bit, and
   exclude mode from the comparison.
7. **The watcher, last and optional.** Raw inotify behind an interface, scan-only everywhere else.
   It never reports a change; it only shortens the wait before the next scan — because the queue
   overflows silently (60,000 files created, 16,385 events readable, 99.97% dropped) and
   `InotifyAddWatch` fails with ENOSPC mid-walk at ~65k directories.

## Refused in writing, in version 1

Hard links, sparse files, owner/group/xattrs, sub-file deltas, three-way merge, and any file being
written while it is synced — a live SQLite file or a VM disk will sync torn.

Sub-file deltas deserve the reasoning rather than the verdict. rsync's own manual turns the delta
off when both ends are local, "the transfer may be faster if this option is used when the bandwidth
between the source and destination machines is higher than the bandwidth to disk". Measured here,
the scan runs at 447 MB/s against a ~110 MB/s LAN, so the delta only pays if it saves more than
about a quarter of the bytes. Two machines on one wire are much closer to rsync's local case than
to the dial-up case the algorithm was written for. blake3 is a tree hash, so per-block digests can
be added later without changing the whole-file digest already on the wire.
