# Access rules

Every namespace has a rule saying who may reach it. The default is nobody: a path with no rule above
it serves no one, and a config that declares none serves no one at all.

```lua
drop.mount("/",        { access = "paired" })
drop.mount("/papers",  { type = "files", dir = "~/papers" })
drop.mount("/scratch", { type = "files", dir = "~/scratch", writable = true, access = { "me" } })
```

## The vocabulary

| written | admits |
|---|---|
| `"anyone"` | any caller at all, paired or not — a public path |
| `"paired"` | any device in the address book |
| `"trusted"` | only the ones you decided to trust |
| `{ "bob" }` | a person: any machine of theirs |
| `{ "bob@laptop" }` | one machine of theirs |
| `{ "buildbox" }` | a machine paired on its own, with no person |
| `{ key = "…" }` | a bare endpoint id that never paired |
| `{ password = "…" }` | whoever presents the secret, whoever they are |

Any one of them is enough, unless `all = true`, which requires every rule declared. There is also
`visible`, which is who may *see* a path without being able to open it — see below.

A password is an argon2id hash, not the word. `drop me passwd` produces one.

```console
$ drop me passwd
password: ••••••••
$argon2id$v=19$m=65536,t=3,p=4$…
```

## How it inherits

Resolving a path walks up until it finds a mount with a rule. A rule written deeper **replaces** the
one above rather than adding to it.

```
/            access = paired          ← everything under here, unless overridden
/work        type = files             ← inherits paired
/work/secret access = { "me" }        ← replaces it: me, and nobody else
```

Replacing rather than combining is deliberate: a rule you can read on one line is a rule you can
reason about. Combining would mean the answer to "who can reach this" is spread over every ancestor.

## Recognising somebody is not letting them in

Pairing with bob grants his machines nothing. It means his phone arrives as `bob@phone` rather than
as a stranger. What it may then reach is whatever the rules say, and the default is nothing.

Trust is the second, deliberate step. Pairing is recognition; `trusted` is what the narrower rules
are written against — a path visible to `trusted` is not visible to somebody you paired with once at
a conference.

```console
drop peer pair <ticket>             # recognition
drop peer trust bob                 # the deliberate second step
```

## Seen, but not open

A path can be visible without being openable. That is a door with a bell on it: the difference
between "there is nothing here" and "you may ask for this".

```lua
drop.mount("/vault", { type = "files", dir = "~/vault", access = { "me" }, visible = "paired" })
```

```console
# on their side
$ drop path ask bob:laptop:/vault --why "the invoice from March"
# on yours
$ drop path requests
$ drop path grant /vault bob
```

A path guarded by a password is in **no** listing at all — nobody offers a secret to ask what
exists — so whoever you hand one to needs the path as well as the word.

Listings are filtered, not refused: you are shown what you could reach and what you could ask for,
and told nothing about the rest.

## Granting and revoking

`drop path grant` and `drop path revoke` write decisions that sit alongside the config, so letting
somebody in does not mean editing a file and restarting.

```console
drop path grant /papers bob
drop path revoke /papers bob
drop peer forget bob@laptop        # stop recognising the machine entirely
```

A refusal is checked before anything admits, and it matches the local name of the device as well as
the person. That last part was a real hole: a device refused by its machine name went on being
admitted the moment it presented a badge naming a person you knew — which it does on every
connection — while the interface still drew it as refused. Admitting matches a bare name only for a
caller with no person, which is right for letting somebody in and wrong for keeping them out.

## What a rule cannot be satisfied by

**What machine somebody is sitting at.** A [plate](identity.md) says which hardware a caller's drop
runs on, and on a machine without a TPM every account there can produce it. It is for a listing to
say what is where, and for nothing else. A test pins this.

**A name the far end chose for itself.** The label off a badge is what a device calls itself. Nobody
vouches for it, and no rule is satisfied by it — it is there so a list can show a machine that has
no local name yet.

## What a guess costs

A password-guarded path costs 64 MiB and three passes of argon2 to try. A caller gets six tries a
minute; getting in somewhere that asked for no password does not buy more, and one guess is hashed
once however many rules ask about it. Both of those are covered in [hardening](security.md).

## Where it lives

`src/pkg/ns/access.go` is the rule and `Admits`. `src/pkg/ns/grant.go` is refusal.
`src/pkg/grant/` is what has been allowed and refused from the interface. `src/pkg/asked/` is
requests waiting for an answer. `src/pkg/passwd/` is argon2id and the per-caller answer cache.
