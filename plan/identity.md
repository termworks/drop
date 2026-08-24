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

## What the user key should be: an SSH ed25519 key

Not a new kind of key nobody has. An **ed25519 SSH key**, because it is a signing key by design,
because most people already have one, and because it covers the whole range of how careful somebody
wants to be with a single mechanism:

| where it lives | what it is | how it signs |
| --- | --- | --- |
| a YubiKey | `ssh-keygen -t ed25519-sk -O resident -O verify-required` | never leaves the hardware; a touch per signature, through ssh-agent |
| a file | `~/.ssh/id_ed25519`, or one drop generates | read and signed directly |

Badges are signed and checked with OpenSSH's own signature format, under a namespace of drop's own:

```
ssh-keygen -Y sign   -f user -n drop badge
ssh-keygen -Y verify -f allowed -I bob -n drop -s badge.sig < badge
```

The namespace matters: a badge signed `-n drop` cannot be replayed as a git commit signature or an
ssh login, and neither of those can be replayed as a badge. In Go this is
`golang.org/x/crypto/ssh` for parsing and verifying, and `ssh/agent` for signing — which is what
makes a hardware key work without drop knowing anything about hardware.

### Why not an age key

Because an age key is for encryption, and the two hardware cases do not survive contact:

- **age on a YubiKey** is a P-256 key in a retired PIV slot, used for ECDH. It cannot sign, and its
  own documentation says so: *use SSH or FIDO2 for that*. Nothing can be derived from it either,
  because the secret never leaves the key — which is the point of it.
- **a file-based age identity** is X25519. Also an encryption key. A signing key can be derived from
  it as seed material, deterministically, and that is worth offering as a shortcut for somebody who
  wants their drop identity to follow a key they already keep. It is a shortcut and not the design:
  rotating the age key would quietly make them somebody else.

## The two decisions

**Where does the user key live?** An ssh key makes this a choice rather than a compromise:

*On a YubiKey.* The strong answer. The key cannot be copied off a stolen laptop, because it was
never on it. Enrolling a machine costs a touch. Losing the key means losing the ability to enrol,
so a second one enrolled at the same time is the backup — which is what people with YubiKeys
already do.

*In a file, on one machine.* The user key stays on the machine that enrols the others. Backed up
by copying a file, which people understand.

*In a file, on every machine.* The shortcut. Every machine can enrol the next, and a stolen laptop
is a stolen identity with no way to take it back.

Support all three, because the mechanism is the same one; say plainly which is which.

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
