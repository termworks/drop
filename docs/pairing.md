# Pairing

Two devices pair once, by key. After that either can reach the other from anywhere — across NATs,
across networks, through address changes — with no account and no server holding your data.

```console
$ drop peer pair                        # on one machine
9363f77d…#qxwo-e62y

$ drop peer pair 9363f77d…#qxwo-e62y    # on the other: done, forever
```

## A code a camera can read

In a terminal the ticket is drawn as a QR code above the text, black on white whatever the
terminal's theme is — a code drawn in a dark theme's own colours comes out inverted, and plenty of
cameras give up on that. Piped, it is only the text. The interface's pairing screen draws the same
code, and the [Android app](../apps/android/README.md) both shows one and reads one.

The code is a link, `drop://pair/<ticket>`, so a phone that reads it with its own camera app opens
drop at the right place, and so does tapping one somebody sent.

## Which node answers

With `drop serve` running, pairing goes through it — offering a code and taking one alike. The
daemon is what the other device reaches from then on, so it is the one whose address the pairing
has to carry: a command that paired with a node of its own would write down that node's address,
which is gone the moment the command exits.

The side that shows the code writes the pairing down *before* it answers, and a side that refuses
says so, so a device never believes it paired with somebody who threw the attempt away. As soon as
a pairing lands, the rendezvous is published for it, rather than on the next five-minute round.

## Pairing is with a person

The exchange carries a **badge**, and both sides write down the other's user key. A machine of
theirs you have never met then presents its own badge and is recognised without pairing again.

> Pair once per person, not once per pair of machines.

```
  you ──── one SSH key ────┬──── laptop     each machine carries a badge:
                           ├──── yubikey    "this user owns this machine,
                           └──── server      called <name>, until <date>"
```

A badge is a statement signed by your user key, in OpenSSH's own signature format under drop's
namespace — so it can be checked with `ssh-keygen -Y verify`, and cannot be replayed as a git
signature or an SSH login. It is signed once, at enrolment, and shown on every connection after
that: a touch per connection would be unusable, and a touch every ninety days is not.

```console
drop peer pair <ticket>             # the user key is learnt; their other machines work later
drop peer pair <ticket> --machine   # this machine and no other
```

`--machine` is for a build server, a box that is nobody's personal identity, or a deliberate refusal
of transitive trust.

## The user key

An ed25519 SSH key, generated on first run if you do not point drop at one, and read through
`ssh-agent` if you do — so a key on a YubiKey, in a PIV slot, or in a file all work the same way.

```lua
drop.user_key = "~/.ssh/id_ed25519"         -- a key you already have
drop.user_key = "~/.ssh/id_ed25519_sk.pub"  -- a YubiKey, signed through ssh-agent
```

The public half is enough when the private one is in hardware. drop cannot talk to a security key —
that is CTAP, PIV or a vendor's protocol, none of which belongs in here — so signing a badge is a
**command**, which reads what to sign on stdin and writes the signature on stdout.

```lua
drop.user_sign = "ssh-keygen -Y sign -f ~/.ssh/id_yubi -n drop"   -- the default, spelt out
drop.user_sign = "my-signer --whatever"                           -- anything that can reach the key
```

Unset, drop works it out: a key it can read is signed in process with no touch; a key it cannot is
signed by `ssh-keygen -Y sign`, which every machine with SSH already has and which drives a security
key directly — no agent involved. A key drop was *pointed at* and cannot find is an error: it will
not answer a typo by inventing a second identity.

## Adding a device, and making it yours

Every device comes in the same way, whoever's it is: added. On one of them `drop add` shows a code
and a QR, and on the other `drop add <code>` takes it — or *Add* on a phone, which shows your code
and scans theirs. The two are paired from then on.

```console
$ drop add
  code:    tevp-spsd-uyle

on the other machine, within 5m0s, run

  drop add tevp-spsd-uyle
```

The code is all anybody types. It is looked up under a key only the code works out, which says
which machine is showing it and what the code is for, so the sixty-four-character id never leaves
the screen, and the relay holding the record learns neither the code nor who asked.

A device on the same network needs no code at all. Every drop says on the local wire who it is and
what it is called, so each lists the devices around it nobody has connected with yet — `drop
nearby`, or *Nearby* on the phone — and picking one asks it. Its person sees the ask, says yes or
no, and both screens show the same six-digit number, so the yes is for this device and not for
another one next to it. Underneath it is the pairing a code makes, with the code handed over on the
connection the two already share instead of by a person.

What a device you added is to you comes afterwards, and is asked of it the same way:

| | |
|---|---|
| `drop promote <name>` | it becomes one of your machines: it wears a badge one of yours signs, and every other machine of yours learns of it |
| `drop join <name>` | this machine becomes one of its machines |

A machine that wears a badge cannot sign one for another, so a phone promoting something has one
of your machines that holds your key do the asking; the number is worked out from your key rather
than from whichever machine asked, so it is the same on all of them. Joining with `--key` from
`drop machine add` hands over the key itself instead: whoever holds it is you, everywhere, and only
an ed25519 key drop can read can leave; one in hardware cannot, which is the point of it.

Either way the key the machine had is set aside beside the new one. A machine whose badge runs out
before it meets one of yours still starts, wearing the stale badge, which proves nothing to anyone.

### One address book

Your machines keep one address book between them. Somebody added from the phone is known to the
laptop, a name or a trust changed on one is changed on all of them, and whatever is removed anywhere
is removed everywhere. Each machine keeps its own book — the secrets it made, where it last saw
everybody — and hands the rest of its machines everything in it the moment anything changes: every
entry says when it was last decided about, every removal is a mark with a time, and of two words
about one machine the newer stands, whichever machine it came from and in whichever order.

A machine you took out of yours is turned away as a stranger by every one of them, whatever badge it
still wears, and none of them writes it back in. Adding it again puts it back.

`drop me leave` takes this machine back out of yours, and `drop me reset --yes` — *Delete everything*
on the phone — deletes everything drop knows on it. Both tell the rest of your machines first, and
each of them forgets it.
### All of them, through any one

Pair a machine with one of yours and it is one of yours to all of them — but it only knows the one
it met. So your machines tell each other the rest. Every few minutes, and the moment a pairing
lands, each says hello to the others it knows, and one of yours answering one of yours adds two
things: every machine of yours it knows of, and the secret all your machines share. A machine it
had not heard of is written down, and the two find each other under a secret each works out from
that one — the same way a paired machine is found, from anywhere, without the two ever pairing.

The shared secret is made by the first of your machines asked for it and handed to the rest. Two
made apart before they met settle on the lower of the two, and every pair's secret moves with it.
Only a machine already known to be yours is believed about which machines are yours.

## Being found without being findable

A device that moved cannot be found at the address its peers wrote down. So drop publishes where it
is — under an identity only the two of you can compute.

```
identity = ed25519(HKDF(pair secret, publisher, hour))
```

| | |
|---|---|
| someone holding your endpoint id | still cannot locate you: the id is not what the record is filed under |
| a device paired with three others | publishes three unrelated records, tied to each other by nothing |
| the identity | rotates hourly, so a relay cannot watch one record over weeks |
| one paired device | cannot observe your availability to another, because the secret is per-pair |

The record holds where the device can be reached: its relay, whatever a relay saw it arrive from,
and the addresses it has on its own networks. That last part is what lets two machines on one wire
or one overlay meet over a link that answers in milliseconds instead of through a relay in another
country — and it costs something, because a record says `192.168.1.24` and whoever can read it
learns which networks you are on. For a rendezvous record that is one paired device and nobody else.
`drop.direct = false` leaves the addresses out; `drop.rendezvous = false` turns publishing off
entirely.

The pair secret itself is derived during pairing over a stream QUIC has already encrypted and
mutually authenticated, mixing both sides' nonces through HKDF, salted with both endpoint ids and
ordered so the two ends compute the same value.

## Finding each other, cheapest first

1. **mDNS**, if both machines are on the same network
2. **the rendezvous**, under the derived identity above
3. **a relay**, when a NAT will not allow a direct connection — with hole-punching upgrading to a
   direct link when it can

The cost of the third is that a relay knows two parties are talking, though not who they are or what
they say. Traffic stays end-to-end encrypted. `drop.relays` points at your own.

## Revocation

Expiry and a local refusal, and nothing more honest is possible without a server.

| | |
|---|---|
| a badge | lasts ninety days, so a lost machine stops being trusted within ninety days rather than today |
| a vouched badge | is signed again only by a machine that still has the phone in its address book: forget it there, and it runs out |
| `drop peer forget bob@laptop` | stops this machine trusting it immediately, and tells nobody else |
| `drop path revoke` | stops one path, immediately, on this machine |

## Another person on one machine

`$DROP_PROFILE` is a whole other identity: its own device key, user key, address book and
conversations, on a port derived from the name so two can run at once. Two profiles are strangers
who must pair — which is how a rule that names somebody else gets tried without a second computer.

```console
$ DROP_PROFILE=bob drop me user     # a different person
$ DROP_PROFILE=bob drop serve       # alongside your own, on its own port
$ drop peer pair                    # then pair them, as you would two machines
```

A profile that sets `drop.user_key` to the same key you use is *you* again — leave it out for a
genuinely separate person.

## Where it lives

`src/pkg/user/` is the user key, the badge and the SSHSIG format. `src/pkg/proto/pair.go` is the
exchange. `src/pkg/rendezvous/` is the derived identity. `src/pkg/discovery/` is mDNS.
`src/pkg/book/` is the address book, including the pair secrets.
