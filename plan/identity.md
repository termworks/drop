# People, and the machines they own

Today drop has one kind of identity: a keypair per machine. Pairing links two *machines*, and an
access rule names *machines*. That is wrong in a way that gets worse with every machine added.

Two people with three machines each is nine pairings, and a rule that says "bob may read /work"
has to be written as "bob's laptop, bob's desktop, bob's phone may read /work" — and rewritten
every time bob buys a computer.

What is wanted is what everybody means when they talk about this: **a person owns machines**, and a
rule names the person, or one of their machines when it matters.

```lua
drop.mount("/work",    { access = { "bob" } })          -- any machine bob owns
drop.mount("/keys",    { access = { "bob@yubikey" } })  -- that one machine
drop.mount("/scratch", { access = { "me" } })           -- any machine I own
```

## What other people did

**`did:key`** is a W3C identifier derived from a public key: self-certifying, resolvable with no
registry, no ledger and no network. It is a good way to *spell* a person's identity. It is not a
way to list their machines: it is derived from one key and says nothing about any other.

**Matrix cross-signing** is the design that solves exactly this problem, and solves it without
asking a server to be trusted. A person has a **master key** which is their identity. It signs a
**self-signing key**, which signs each of their own devices. A device is one of bob's if bob's
self-signing key says so. Verifying bob once — in person, over a QR code — means every machine bob
signs is trusted from then on, with no further ceremony.

**UCAN** goes further: a person's identity delegates *capabilities* to a machine's identity, as
chained tokens the holder presents. That is a different shape from drop's, where the serving node
decides from its own config what a caller may reach. Adopting it would mean rewriting the access
model rather than extending it.

## What drop should do

Matrix's shape, without the parts that exist for a server.

**A user key.** One ed25519 keypair per person, at `$XDG_CONFIG_HOME/drop/user`. The public half
is the person's identity, printed as hex like everything else here, and exportable as
`did:key:z…` for anybody who wants the standard spelling.

**A badge.** Each machine keeps a statement signed by the user key:

```
this user <user pub> owns this machine <device pub>, called "laptop", until <date>
```

The transport already proves the *machine*: iroh authenticates the device key on every connection.
The badge is what turns "some machine" into "a machine of bob's", and it is checked against the
user key already in the address book. No third party is asked anything.

**Pairing becomes person to person.** The ticket carries the user key. What is written down is a
person, with the machines of theirs that have been seen:

```
bob  <user pub>  shared secret
     laptop   <device pub>
     desktop  <device pub>
```

A machine of bob's that has never been met presents a badge, and is recognised without pairing
again. That is the whole point: **pair once per person, not once per pair of machines.**

**Access rules gain a person.** `ns.Caller` grows `User` and `Device`. `"bob"` matches any machine
carrying a valid badge from bob. `"bob@laptop"` matches that machine. `"me"` matches this user's
own machines, which is what makes one config work on all of them.

## The two decisions

**Where does the user key live?**

*Copied to every machine.* Simple: every machine can enrol the next one, and there is nothing to
lose. But a stolen laptop is a stolen identity, and there is no way to take it back.

*Kept on one machine.* `drop enrol` on the new machine shows a code; a machine that has the user
key signs its badge. The user key never travels. Losing that machine means losing the ability to
add machines, so `drop user export` has to exist and has to be used.

The second is the honest one, and the first is what people will do anyway. Support the second and
document the first as the shortcut it is.

**Revocation, which has no good answer without a server.** A badge that has been issued is good
until it expires. The options are all uncomfortable:

- **Expiry.** Badges last ninety days and are re-signed by a machine holding the user key. A lost
  machine stops being trusted within ninety days, not today.
- **A local refusal.** `drop peers rm bob@laptop` stops *this* machine trusting it, immediately,
  and says nothing to anybody else.
- **Gossip.** Devices pass revocations to each other when they meet. Real, and a project of its own.

The first two are worth building and the third is worth writing down and not building yet. What
must not happen is pretending revocation works when it does not.

## Getting there without breaking what works

1. **The user key and badges, ignored by everybody.** Generate on first run, sign the local device,
   change nothing else. Nothing depends on it yet.
2. **Carry it.** Hello and pairing exchange user keys and badges. `ns.Caller` gains the fields.
   Rules still match the way they do now.
3. **Rules learn people.** `"bob"` and `"bob@laptop"` and `"me"`. Machine names keep working, so
   every config already written keeps working.
4. **The address book learns people.** Existing entries become a person with one machine and no
   user key — a *legacy* pairing that still works on the device key alone. Nothing has to be
   re-paired.
5. **The interface learns people.** The list groups by person: your machines under `ME`, each other
   person's machines under their name.

Step 5 is the one you can see, and it is last on purpose: the other four have to be right first.
