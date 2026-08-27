# Pairing

Two devices pair once, by key. After that either can reach the other from anywhere — across NATs,
across networks, through address changes — with no account and no server holding your data.

```console
$ drop peer pair                        # on one machine
9363f77d…#qxwo-e62y

$ drop peer pair 9363f77d…#qxwo-e62y    # on the other: done, forever
```

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
