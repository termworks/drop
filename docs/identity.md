# What names a machine

Two questions, two keys. *Whose* is a user key — an SSH key, covered in [pairing](pairing.md).
*Which machine* is the endpoint key, and where that one comes from decides two things you care
about: whether a machine survives being wiped, and whether two people with accounts on one box are
one machine or two.

drop takes it from the machine itself, strongest source first, and says which one it got.

```console
$ drop me machine
8c4d031fa62eca685be2f2ecf784843c42075e7a9935c3fa84b09983d3d10ca5

  named by      the serial on the drive the system is on
  which reads   e5535c77824b
  and by        where this drop keeps its things, so every account and
                profile here is reachable as itself
  survives      a reinstall, because nothing about it is written down
  changes if    the drive the system is on is replaced, or this drop is moved to another home
```

## The sources

| source | what it is | what changes it |
|---|---|---|
| the TPM | a key the chip derives and never hands out | clearing the TPM |
| the board | the serial the firmware holds — DMI on a PC, the device tree on a board | replacing the board |
| a drive | the serial on the drive `/` is on, followed down through partitions, LVM and encryption to the metal | replacing that drive |

Every source is tried and the best one that answers wins, rather than the first: a machine with a
TPM should not be named after a drive because the drive answered sooner.

The drive case is the fiddly one. `/` is usually a partition, often on LVM, often on LUKS, and none
of those have a serial — the kernel made their names up and a reinstall makes up different ones. So
the walk starts at the device `/` is actually on and follows `slaves/` links down until it reaches
something with a serial written on it by a manufacturer. A mirror is named by both of its halves, so
losing one at boot does not rename the machine.

**The TPM path has never run.** Neither machine this was built on has one. A PC whose firmware
offers one (Intel PTT, AMD fTPM) usually ships with it switched off in the BIOS. `metal.Seal` and
`metal.Unseal` are written for the same chip to encrypt with later and carry the same warning: code
that has not executed is not code that works.

## The seed

```
seed = KDF(purpose, what the hardware said, where this drop keeps its things)
```

Nothing is written down when a machine will name itself, which is the point: **reinstall and it
comes back as the machine it was**, with no backup and nothing to restore. Carry a backup to another
box and it does *not* come back as that machine, because the key was never in the backup.

The second half is in there because a machine is one machine and the drops running on it are
several — every account with one, every profile under an account, every node a test brings up. Each
has to be reachable as itself, and two of them deriving one key would not be one machine with two
people on it, it would be two programs answering to one address, which is nobody.

| | |
|---|---|
| same place after a wipe | the same name |
| two accounts, two profiles, two test nodes | different names |
| the same place on another box | a different name |

Every piece that goes into the hash is length-prefixed, and the machine key and a drop's key are
told apart by a slot of their own rather than by a value in a shared one. Both of those were put in
after a test showed that without them two different machines could hash to one name.

## What this is not

On a machine without a TPM, the serial is one every account there can read — so every account there
can derive the machine key. **Machine identity locates hardware. It is never a wall between the
people sitting at it.** Who somebody is stays with their user key, which is where the namespaces are
owned anyway, and no access rule is satisfied by what machine a caller is at. That last sentence is
pinned by a test.

## One machine, several people

Each account has its own endpoint key, so each is reachable as itself. What ties them together is a
**plate**: a statement signed by the *machine* key — the one every account there derives — saying
"this endpoint is one of the drops running on me, and it belongs to that account".

```
  one machine ──── machine key ────┬──── alice's drop    "endpoint E is on me,
                                   └──── bob's drop       account alice"
```

It rides on every connection next to the badge and answers a different question. The badge says
whose drop this is; the plate says what it is sitting on. A peer learns that two endpoints it can
reach are two people at one machine rather than two unrelated machines.

Because every account on a machine can produce that machine's plate, no access rule is satisfied by
it. It is there so a listing can say what is where, and for nothing else.

`$DROP_PROFILE` works the same way — it keeps its things somewhere else, so it gets a name of its
own and is a stranger to the account it runs beside, which is what it is for.

## Changing over

A machine that has been running with a key of its own keeps it. Deriving a different one on an
ordinary upgrade would break every pairing that names that machine without anybody asking, so a
machine already installed says so instead and nothing changes until you say it should.

```console
$ drop me machine rebind
this machine is 2e0b421c044d
it would become 9983d3d10ca5, named by the serial on the drive the system is on

every machine paired with this one knows it by the old name and will not
recognise the new one. Each pairing has to be made again.

run it again with --yes to go ahead.
```

The old key is kept beside the new one as `identity.was`, not deleted.

A machine's key is minted exactly once, however many drops start at the same moment. Two of them
each making one would leave whichever wrote second owning the file while the other ran its whole
session signing as a key that is not there.

## Moving to another machine

A name taken from the hardware stays with the hardware. That is the point of taking it from the
hardware, and it is also the one thing that has to be possible anyway.

So the old machine says it, while it still can, and signs with the key everybody already knows it
by: **its own endpoint key**. Nothing new has to be trusted, because the old name *is* the key that
signed the statement.

```console
# on the new machine
$ drop me id
39ce1a4bee9e4275e641045f9b867c6f60dbd4e801f1719029fc1ba8a4ed1a91

# on the old one, while it still runs
$ drop me machine migrate 39ce1a4bee9e…
2e0b421c044d is now 1ba8a4ed1a91

drop1AMRkcm9wLWhhbmRvdmVyLzEKd2FzIDU2MjllNDhj…

# back on the new machine
$ drop me machine took drop1AMRkcm9wLWhhbmRvdmVy…
  2e0b421c044d said it became this machine
```

Restart drop there afterwards: the handover is picked up when it puts its badge on.

From then on the new machine carries it on every connection — there is no telling which peers have
heard yet — and each of them points what it had filed under the old name at the new one. The entry
keeps **the local name, the shared secret, whose machine it is, and whether it is trusted**; only
the addresses go, because the machine is somewhere else now.

```console
# on a peer, in the daemon log
tron is now 1ba8a4ed1a91, and was 2e0b421c044d
```

It travels on a `hello` as well as on an open, because `drop path ls` is how most peers hear from a
machine first.

Two things must hold before a peer acts on one, and the second is the one that matters. It has to be
signed by the machine it says it was. And it has to name **the very caller presenting it** as what
that machine became — otherwise anybody who overheard a handover could present it and be taken for
somebody else's machine. It runs out after seven days, names one successor, and moves one account. A
move onto an id already in the book is refused: that would make two entries one.

## Where it lives

`src/pkg/metal/` reads the hardware. `src/pkg/plate/` is the stamp and the handover.
`src/pkg/node/identity.go` decides which key this drop uses. `src/cmd/machine.go` and
`src/cmd/migrate.go` are the commands.
