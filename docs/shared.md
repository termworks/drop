# One thing, several machines

Some namespaces are held by more than one machine at once, and every holder can change them. A
`note` is a file several people write; a `files` folder is a directory several people work in. What
makes that work is a record of what happened, signed, that any two holders can bring level.

```console
$ drop path create /standup note --set file=~/notes/standup.md --access paired --share --keep
/standup is up, and written down

$ drop path join tron:/standup --at /standup --set file=~/notes/standup.md
```

The share id is what both machines call the same thing. A namespace is one machine's own word for
it; the id is what two of them are talking *about*, so two machines that spell the path differently
still meet about one thing.

## The record

Every change is signed and names the changes its author had seen.

```
        ┌── B ──┐
   A ───┤       ├─── D          A change names its heads: what came before it,
        └── C ──┘               as far as its author knew.
```

| | |
|---|---|
| id | a digest of the signed bytes — so a change cannot be edited and stay itself |
| author | the user key that signed it |
| about | which namespace it belongs to |
| heads | what its author had seen |
| body | the whole of what they saved |

The `about` field is not decoration: without it a change signed for one namespace could be replayed
into another. A change that names a different thing is refused.

Reading the record back is a topological sort, with ties broken by the smallest id — so which
changes were ready first depends on the order they arrived, and **which of them is chosen does
not**. Every machine reads the same order out of the same set.

A change carries the whole of what its author saved, not a difference against what they started
from. What somebody saved is a whole file, so that is the one thing known to be true about it; a
difference would first have to be applied to the version it was taken against before two versions
could be merged against each other, which is two merges where the job has one. The cost is a copy
per save, which is what folding is for.

## Putting versions back together

Two people changed the same file. Three inputs — what it was, what each of them made of it — and a
three-way line merge, the same shape `git merge-file` uses.

```
  base ──┬── ours   ──┐
         └── theirs ──┴──► merged, or a conflict written into the file
```

Lines that only one side touched are taken. Lines both sides touched, differently, become a conflict
in the file with git-shaped markers, and the row for that namespace says so:

```console
$ drop path ls
  /standup   note   ~/notes/standup.md   unsettled: key we+w+py/ and key o0/sglXm
```

The marker names a *key*, not a person, and that is deliberate: two machines must write identical
bytes or they would never agree that the file is settled. The listing says the same thing the file
says, so what you read in one matches the other.

Something that is not text is never merged as lines. One version becomes the file and the other is
kept beside it under a name saying whose it is.

The merge is checked against the real thing: **30,000 seeded random merges run against
`git merge-file`**, failing on any silent divergence or spurious conflict.

## Noticing a save

A note is a real file. drop watches for a change, and a save is only taken once the file has been
left alone briefly — a writer that empties a file and then fills it, or has written half of what it
means to write, looks exactly like a finished save until you look at when it was last touched.

What drop itself wrote is remembered, by digest *and* by which changes it was written from, so
reading it back is not mistaken for somebody having edited it. Without the second half, a save would
name as "seen" a change that landed a moment ago and is not in the file — and a version claiming to
come after a change it does not contain buries that change on every machine.

A save is heard rather than waited for: an inotify watch nudges the round, with the timer kept as a
backstop, because inotify misses things — watch limits, network filesystems, a directory replaced
wholesale. Local detection is about 500 ms, which is the settling period.

## Keeping it small

A note saved a thousand times would otherwise be a thousand copies of the file. Once **everybody
still remembered has seen all of it**, the whole record is replaced by one change carrying what it
all came to.

The body of that change is the archetype's, not drop's: only the archetype knows what its changes
came to, and a snapshot drop invented would be a second answer to what a history means.

A `files` folder cannot fold the way a note does — a folder does not fit in one change. So the
snapshot drops the bytes it carries inline and keeps the paths and their digests, which is still the
whole truth about what the folder holds; the bytes are fetched the way a big file always was. A
folder whose paths alone will not fit is left unfolded, which costs a longer record and loses
nothing.

Nothing is folded while a peer is still behind, which is what "everybody still remembered" means. A
peer nobody has heard from in a long time is dropped rather than waited for.

## Catching up

```
  A                                   B
  ──                                  ──
  here are my heads      ──────►
                         ◄──────      I have these, and not those
  the ones you are missing ────►
                         ◄──────      taken
```

That happens when a connection arrives, when a change is made, and on a timer — because running it
when there is nothing to say costs a few identifiers.

A change signed by somebody your rule does not admit is refused, whoever relayed it, **and so is
everything made after it**. Membership is not a list drop keeps: it is the access rule read against
your address book. So somebody holding a namespace whom your rule does not name is somebody whose
changes are passed over, and `drop path ls` says so on the row.

## Bytes must be what was asked for

A file too big to travel inside a change is fetched from whoever has it. The digest in the signed
change is checked against what arrives. It is the one account of that version that whoever is
sending the bytes could not have made up — and a holder is not always the author. Bytes that do not
match are dropped and the round tries somebody else.

## Where it lives

`src/pkg/history/` is the signed record and its ordering. `src/pkg/weave/` is the three-way merge.
`src/pkg/meet/` is two machines catching up. `src/pkg/among/` works out who else holds a thing from
the access rule. `src/pkg/arch/note/` and `src/pkg/arch/files/` are the two archetypes that use all
of it. `src/pkg/nudge/` is the inotify watch.
