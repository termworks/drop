# Setting up three machines and one you

A walkthrough of a real setup: a laptop called **core**, a workstation called **tron**, and an
NVIDIA Orin called **orin**. One identity — a YubiKey — across all three, so every machine is *you*
and a rule that names you covers all of them.

By the end: any of the three can reach any other, from anywhere, and adding a fourth is one command.

```
                  ┌── core   (laptop)
   one YubiKey ───┼── tron   (workstation)
    = one you     └── orin   (the little ARM one)
```

---

## 1. Install it

On each machine. The binary is static and cross-compiles, so the Orin gets the same build from
whichever machine you build on.

```console
# on the machine you build on
make build
install -m 0755 drop ~/.local/bin/

# for the Orin, from the same place
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -o drop-arm64 ./src
scp drop-arm64 orin:.local/bin/drop
```

Check it runs everywhere before going further:

```console
$ drop --version
```

---

## 2. Your identity: the YubiKey

This is the part that decides everything after it, so do it first and do it once.

drop's identity is an **SSH key, per person, not per machine**. Put the same one on all three and
they all answer to `me`. Let each machine make its own and they are three strangers who have to pair
with each other pairwise — which works, and is not what you want.

### Make it resident on the key

```console
$ ssh-keygen -t ed25519-sk -O resident -O application=ssh:drop -C "bresilla"
Generating public/private ed25519-sk key pair.
You may need to touch your authenticator to authorize key generation.
Enter file in which to save the key (/home/bresilla/.ssh/id_ed25519_sk):
```

Two files come out, and neither is the secret:

| | |
|---|---|
| `~/.ssh/id_ed25519_sk` | a *handle*. Useless without the YubiKey in your hand |
| `~/.ssh/id_ed25519_sk.pub` | the public half |

`-O resident` is the flag that makes the rest of this guide easy. It stores the handle **on the
YubiKey itself**, so on any other machine you plug in and ask for it back rather than copying files
around. Without it you would have to copy `id_ed25519_sk` to each machine by hand.

`-O application=ssh:drop` names it, so it sits beside any other resident keys you have instead of
colliding with them.

### Point drop at it

```lua
-- ~/.config/drop/init.lua
local drop = require("drop")

drop.user_key = "~/.ssh/id_ed25519_sk.pub"
```

The **public** half. drop never reads a private key it does not need to: for a hardware key it hands
the signing to `ssh-keygen -Y sign`, which drives the YubiKey directly.

### What a touch costs, and when

Worth knowing before you commit to this, because the two answers are very different:

| | how often |
|---|---|
| the **badge** — "this machine is mine" | once per machine, every 90 days |
| a **change to a shared note or folder** | once per save |

The first is nothing. The second is real: if you keep a `note` or a shared `files` folder, every save
signs a change, and a touch-required key means a touch every time. Three ways out, pick one:

```console
# a key that does not ask for a touch — convenience for security, deliberately
ssh-keygen -t ed25519-sk -O resident -O no-touch-required -O application=ssh:drop
```

…or run an agent (below) and accept the touches, or use a plain file key on machines that do a lot
of shared editing and keep the YubiKey for the laptop.

### The agent

Badge signing does not need an agent. Change signing does — drop asks the agent for a signer that
matches the public key you named.

```console
$ eval "$(ssh-agent -s)"
$ ssh-add -K              # load resident keys from the YubiKey
```

If it is not running you will see it plainly, rather than something mysterious:

```
that key is held by an agent, and no agent is running
```

---

## 3. The first config

```console
$ mkdir -p ~/.config/drop
$ $EDITOR ~/.config/drop/init.lua
```

Start with the smallest thing that is useful. This is `core`:

```lua
local drop = require("drop")

drop.user_key = "~/.ssh/id_ed25519_sk.pub"
drop.name = "core"

-- Nothing is reachable until a rule says so. This one line is what makes the rest serve anybody.
drop.mount("/", { access = "paired" })

drop.mount("/inbox", { type = "share", dir = "~/Downloads" })
drop.mount("/chat",  { type = "chat" })
```

Check it before relying on it — a config that does not parse is fatal, and the error names the line:

```console
$ drop topic
$ drop me key
  your key  SHA256:…  a YubiKey, ~/.ssh/id_ed25519_sk.pub
```

That fingerprint is **you**. The same line should appear on all three machines when you are done.

`drop me key use ~/.ssh/id_ed25519_sk.pub --yes` writes the `drop.user_key` line for you, and signs
this machine's badge with the key there and then — a touch on the YubiKey.

---

## 4. Keep it running

```console
$ install -m 0644 misc/drop.service ~/.config/systemd/user/
$ systemctl --user enable --now drop
```

Without a daemon a machine is only reachable while you are running something that serves. On the
laptop you might not want that; on `tron` and `orin` you do.

---

## 5. Make the other machines you

This is the step the resident key was for. On **tron**, with the YubiKey plugged in:

```console
$ ssh-keygen -K
Enter PIN for authenticator:
You may need to touch your authenticator to authorize key download.
Saved ed25519-sk key ssh:drop to id_ed25519_sk
```

That writes `id_ed25519_sk` and `id_ed25519_sk.pub` into the current directory. Move them where drop
expects:

```console
$ mv id_ed25519_sk id_ed25519_sk.pub ~/.ssh/
$ chmod 600 ~/.ssh/id_ed25519_sk
```

Then the same two lines in that machine's config:

```lua
drop.user_key = "~/.ssh/id_ed25519_sk.pub"
drop.name = "tron"
```

Repeat on **orin**. Then check all three agree:

```console
$ drop me key                    # on each machine — the same fingerprint
  your key  SHA256:…  a YubiKey, ~/.ssh/id_ed25519_sk.pub
```

**Nothing secret was copied.** The handle files are useless without the YubiKey; the identity itself
never left it.

> Doing this without a resident key? Then copy `~/.ssh/id_ed25519_sk` and `.pub` to each machine
> over `scp`. Same result, more moving parts, and files you have to remember you moved.

---

## 6. Pair them

Even with one identity, two machines still have to meet once — pairing establishes the shared secret
that lets them find each other later without publishing anything anybody else can read.

On **core**:

```console
$ drop machine add
  code:    qxwo-e62y-k3fa
```

On **tron**, with that code:

```console
$ drop machine add qxwo-e62y-k3fa
```

Now **core ↔ tron**. Do it once more for **core ↔ orin**.

### tron ↔ orin comes free

Your machines tell each other about the rest of your machines, so within a few minutes `tron` and
`orin` know each other without ever meeting:

```console
$ drop machine ls
  core   this one  ec325aa2…
  tron   paired    9363f77d…
  orin   paired    a9620d59…
```

Without the YubiKey setup of step 5, `drop machine add` makes the new machine yours in the same
step — it is handed a badge `core` signs — so step 5 is only for keeping the key in hardware.

---

## 7. Check it actually works

```console
# what does orin share with you?
$ drop topic ls orin

# send it a file
$ drop connect orin:/inbox ~/notes.pdf

# talk to it
$ drop connect orin:/chat

# put a folder on it, from here: it lives in ~/drop/papers over there
$ drop topic add papers folder --on orin

# or just look around
$ drop
```

Addresses read from the right, so `orin:/inbox` is "the machine called orin, its /inbox". Add a
person on the left when the name is ambiguous: `bresilla:orin:/inbox`.

---

## 8. Now serve something worth serving

With all three as `me`, a rule naming `me` covers every machine you own — which is the whole reason
for one identity.

```lua
-- on tron: a folder you can work in from the laptop
drop.mount("/work", { type = "files", dir = "~/work", writable = true, access = { "me" } })

-- on orin: what it is actually doing
drop.mount("/logs", { type = "stream", command = "journalctl -f -n 50", access = { "me" } })
drop.mount("/term", { type = "tty", input = true, access = { "me" } })
```

```console
$ drop file ls tron:/work
$ drop file get tron:/work/paper.tex
$ drop connect orin:/logs
$ drop connect orin:/term
```

`writable` and `input` are one flag each, not one per operation: `writable` means upload, mkdir, move
**and delete**; `input` means they can type. Both are written against `me` here, which is you.

### A note all three can edit

```console
# on core
$ drop path create /standup note --set file=~/notes/standup.md --access me --share --keep

# on tron and orin
$ drop path join core:/standup --at /standup --set file=~/notes/standup.md
```

Edit `~/notes/standup.md` in any editor on any of the three. Saves are noticed, signed and merged.
This is the one that costs a YubiKey touch per save — see §2.

---

## When something does not work

| what you see | what it means |
|---|---|
| `that key is held by an agent, and no agent is running` | start `ssh-agent` and `ssh-add -K` |
| `no key at ~/.ssh/id_ed25519_sk.pub` | you pointed at a key that is not there — drop refuses to invent a second identity |
| `malformed unsigned varint` | the two machines are on different builds; deploy the same one to both |
| a peer never resolves | `drop peer whois <name>`, then `drop serve` on the far end — a machine with no daemon is only reachable while something there is serving |
| nothing is listed | a path with no rule above it is reachable by nobody. Is `drop.mount("/", …)` there? |

## Where to go next

| | |
|---|---|
| [Access rules](access.md) | past `paired` and `me`: people, machines, passwords, and seen-but-not-open |
| [The archetypes](archetypes.md) | what each kind of namespace is for |
| [One thing, several machines](shared.md) | how shared notes and folders actually work |
| [What names a machine](identity.md) | machine identity, and replacing a machine without pairing again |
| [Configuration](config.md) | everything `init.lua` takes |
