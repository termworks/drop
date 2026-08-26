# Several people changing one thing

Not file synchronisation. Several people, on several machines, changing one thing at the same time
and converging, with no server. Some archetypes have this and some do not: a note does, a terminal
does not.

## What Anytype actually does

The inspiration is worth stating precisely, because the common picture of it is wrong.

any-sync is **not a CRDT library**. It is a signed, content-addressed **change DAG** per object.
A change names the heads it saw, its id is the hash of its own signed bytes, and every peer derives
the same linear history by topological sort with ties broken on change id. That is about forty
lines of struct, not a research project.

And **any-sync defines no merge semantics at all**. The payload is opaque and encrypted; only the
metadata is plaintext. What the ordered changes *mean* is the application's business.

That is the same boundary drop already draws. The sync layer orders; the archetype interprets.

Two more things that were checked rather than assumed:

- **Anytype does not merge text character by character.** A block's text is carried as one whole
  string (`BlockSetText{ string value }`), so concurrent edits to one paragraph are last-writer-wins
  under the deterministic order. Block-granular, not Google Docs.
- **The one thing any-sync needs a server for is permission.** Its access list is a linear chain
  counter-signed by a network key, and a permission change without connectivity is refused. Objects
  need no server; the ACL does. drop should not inherit that half — it already has access rules.

## What drop already has, and the two things it does not

Have:

- a durable, encrypted, append-only, deduplicating log — `src/pkg/convo`
- reliable pairwise delivery with an outbox and an acknowledgement of what actually landed
- a transport where a machine behind NAT is reachable over a connection it opened — `dial.Kept`
- real signing, reusable as-is: SSHSIG under the namespace `drop`, and badges already binding a
  user key to a device, so "this change was authored by this person" is one `Sign` call away

Missing, and these are the two that matter:

1. **Causality and content-addressed identity.** A `convo.Message` has no parent and no signature,
   its id is a sender-minted ULID unbound to its bytes, and ordering is by the sender's wall clock.
   Fine for two people chatting; wrong the moment three machines edit one thing.
2. **An archetype cannot initiate anything.** The interface is Name/Version/Read/Note/Serve, no
   package under `src/pkg/arch` imports `dial`, and the daemon's push loop is hardcoded to `/chat`
   in four places.

And one place a genuinely new concept is needed: an `ns.Mount` is one machine's local declaration
with no identity for the instance and no list of who else holds it, so "the same note on five
machines" is today five unrelated namespaces that happen to share a spelling. ARCHESPACE does not
forbid an instance identity; it simply never names one.

## The decisions

| | |
| --- | --- |
| what is collaborative | an archetype says so: `note` (new), `files`, `chat`. Not share, link, tty, stream |
| who may change it | the access rule already says. Reaching it is changing it |
| revocation | not retroactive — what someone already has, they keep. Same as Anytype, same as drop's existing refusals |
| how you edit | a real file, your own editor. Merging happens at save |
| merge grain | line-wise for text, keep-both for anything else |
| history | a means, not a feature. Folded once every peer has caught up |
| a peer that never returns | forgotten after a while; if they come back they take a snapshot, not the log |
| how it starts | discovery. It appears in `drop path ls` because the rule names you, and you join it |

## The shape

**A change** is `{ heads[], author, signature, payload }`, its id the hash of its signed bytes.
Ordering is topological with ties on id. The payload is opaque to everything but the archetype.
This is any-sync's model, minus the parts that exist only because Anytype runs a public network.

**A namespace gains an identity** — the thing five machines mean when they say they hold the same
note — and a set of peers, which is what the access rule already names.

**The archetype interprets.** `note` reads the ordered changes as edits to a file. `files` reads
them as "this path now has this digest", with the bytes moving over the transfer path that already
exists. `chat` already converges and mostly needs to be told that it counts.

**Merging is the archetype's, not the log's.** Line-wise three-way for text, keep-both otherwise —
and the rule for which is which has to be written down, because drop will sometimes get it wrong
and destroying a SQLite file by merging it line-wise is not a recoverable mistake.

## Order of work

1. **The change log**: signed, content-addressed, causal. Its own package, no networking. This is
   where `convo`'s data model has to change rather than be reused — the seal binds a record to a
   *peer*, and this one is keyed by *object*.
2. **Instance identity and the peer set** on a namespace, and `drop path join`.
3. **Letting an archetype speak first.** The smallest honest version of "tell everyone who is
   interested", built on `dial.Kept`, which already holds connections both ways.
4. **`note`**, the simplest collaborative archetype: one file, line-wise merge.
5. **`files` collaborative**, which is where the five known defects have to be fixed first —
   `Entry.At` is whole seconds, `put` cannot replace, nothing calls `Chtimes`, mode is flattened,
   and there is no resume.
6. **Compaction**, once the peer set is known and a peer can be forgotten.

## Refused, in writing

Character-level text merging. Three-way merge of anything that is not lines. Merging a file that is
being written. Retroactive revocation — removal gives forward secrecy only, and you cannot un-share
bytes somebody already has. A server, of any kind, for any part of it.
