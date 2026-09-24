# drop on Android

The whole node, on a phone. The Go core is compiled to a native library and bound for the JVM; the
Kotlin around it draws, and decides nothing.

```
  apps/android/
    mobile/     Go — the binding gomobile turns into an AAR
    app/        Kotlin — Compose screens, one foreground service
    libs/       where the built AAR lands (not in git)
```

## Why it is shaped this way

Everything drop knows how to do is already in Go, and it compiles for Android unchanged — iroh,
QUIC, the archetypes, the lot. Rewriting any of that for the phone would mean two implementations
of one protocol, and they would drift.

So the phone runs the same code, and more than that, the same *entry point*: `mobile.Start` calls
`cmd.Interface`, which is what the full-screen interface on a computer runs on. The node the phone
brings up serves, keeps a connection to each device it reaches, pushes what is queued, and pairs —
the way `drop` in a terminal does, because it is the same function.

`mobile/` is the whole surface between the two sides. gomobile binds strings, numbers, bools, byte
slices and interfaces, so a list crosses as JSON and a callback is an interface Kotlin implements.

## What it does

| | |
|---|---|
| **You first** | the app opens on one list: *Me* first, holding this phone and every other machine of yours, then everybody you paired with, each showing what they sent that you have not read. Settings — the phone's name, whose it is, its folder — are under the ⋮ menu |
| **People, then machines, then paths** | the same arrangement as the terminal interface: who, which of their machines, and what it shares with you |
| **Pairing** | *My code* shows a QR and a short code the other device scans or types; *Scan theirs* reads one off a computer's `drop peer pair` or another phone, or takes the code typed in. A `drop://pair/…` link opens the same way |
| **One of your machines** | run `drop machine add` on a computer of yours, then *Add a machine → Scan* here, or type its short code: the phone is yours from then on, and every other machine of yours hears of it. A phone holding your key can show a code instead, for `drop machine join <code>` on a computer. See [pairing](../../docs/pairing.md#making-a-machine-yours) |
| **Chat** | a conversation per machine, kept on both devices. A message for a machine that is off waits and goes when it is back; a message that arrives while the app is closed is a notification |
| **Files** | walk a `files` namespace, download with a tap, upload into one that is writable; long press to rename or delete, and make folders in it |
| **Notes** | read a `note` where it is held, or keep a copy on the phone and write in it. The copy is kept level with every other machine holding it, and what two people type at once is merged |
| **Shared folders** | keep a copy of a folder several machines hold in `Download/drop-kept/`, followed both ways |
| **Handing things over** | *Send files* to a machine's share, or share from any app on the phone and pick who it goes to: files go to its inbox and show in the conversation, text is said in the conversation, and a link can be said or opened over there. A file that arrives is a notification that opens it, and a link that arrives opens — at once with the app on screen, from a notification otherwise |
| **Terminals and streams** | a `tty` or a `stream` drawn live, in colour, cell by cell on the phone's own grid — which is the size the far end is told, again when the keyboard opens, the phone turns or the text is pinched. Typing goes straight into the terminal, as in Termux, with its two rows of extra keys: ESC, TAB, sticky CTRL and ALT, arrows, HOME, END, page up and down |
| **Links** | send one to a machine's `link` namespace and it opens over there |
| **Who may open what** | every path on this phone and on each of your machines stands on a step — only me, trusted, paired, public — changed from the phone and applied on that machine at once, with people let in or kept out by name, answers to who asked, and whether others may see it and ask. On somebody else's machine, ask to be let into what you can see but not open |
| **Managing people and machines** | each of your machines has a ⋮ on its row to rename it or take it out of your machines — taken out on every one of them, not only here. Somebody else has *Rename*, *Remove* and *Trusted* on their screen, and *What they can open*: every path on this phone and each machine of yours, with a switch that lets them in or keeps them out there. The phone itself can leave your machines from Settings |
| **The phone's folder** | share `Download/drop/` as a path called phone, whose step is chosen like any other, and let them put things in it when that is on too |

What arrives lands in `Download/drop/`, where the phone's own file manager finds it.

## Installing and updating

Every release carries `drop-<version>-android.apk`, signed with one key that never changes, so each
installs over the last as an update and keeps its pairings. The easiest way to follow them is
[Obtainium](https://github.com/ImranR98/Obtainium): add an app, give it
`https://github.com/termworks/drop`, and it offers each new release as an update.

## Building it

Needs the Android shell, which carries a pinned SDK, NDK, gomobile, JDK and Gradle:

```console
nix develop .#android
make aar          # the Go core → apps/android/libs/mobile.aar
make apk          # the AAR + Kotlin → an installable, signed APK
make install-apk  # onto a plugged-in device
```

`make apk` depends on `make aar`, so the second command alone is enough.

## Trying it without a phone

```console
make emulator     # an x86_64 Android 15 device, headless
make install-apk
make screen       # a window onto it, from the default shell
```

`make screen` runs scrcpy under nixGL, which is what lets a program built by Nix draw with this
machine's own GPU driver.

## What Android takes away

| | |
|---|---|
| **Background execution** | a node nobody can reach is not a node, so it runs as a foreground service — which costs a permanent notification. There is no way around that on modern Android. |
| **Storage** | the node's own state lives under the app's private directory, handed to Go as `XDG_CONFIG_HOME` and `XDG_DATA_HOME`. Uninstalling takes the identity with it. |
| **Identity** | a phone has no readable board or drive serial, so the key is generated once and written down rather than derived from the hardware. See [identity](../../docs/identity.md). |
| **Doze** | the OS may still stop the process when the screen has been off a long time. The service restarts, and the node comes back as itself. |
| **Its own addresses** | Android refuses an ordinary app the netlink socket Go asks for to list interfaces, so the node cannot see the addresses it has. It is reached through its relay and the rendezvous, which works from anywhere and costs a hop on a shared wire. |

The last one is visible in `logcat` as a repeating `avc: denied { bind } ... netlink_route_socket`
(`b/155595000`). It is a platform restriction rather than a bug here, and the way out is to ask
Android for the addresses through `ConnectivityManager` in Kotlin and hand them to the Go side —
which is not done yet.

## The APK from CI

The `android` job in `.github/workflows/release.yml` builds it in the same flake shell and uploads
it as an artifact on every push, and a tag attaches it to the release. Its version code is worked out from the version — 0.4.3 is 403 — so
each release is a larger number than the last, which Android needs to take it as an update.
