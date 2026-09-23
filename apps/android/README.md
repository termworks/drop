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
| **People, then machines, then paths** | the same arrangement as the terminal interface: who, which of their machines, and what it shares with you |
| **Pairing** | *My code* shows a QR the other device scans; *Scan* reads one off a computer's `drop peer pair` or another phone. A `drop://pair/…` link opens the same way |
| **Chat** | a conversation per machine, kept on both devices. A message for a machine that is off waits and goes when it is back; a message that arrives while the app is closed is a notification |
| **Files** | walk a `files` namespace, download with a tap, upload into one that is writable |
| **Handing things over** | *Send files* to a machine's share, or share from any app on the phone and pick who it goes to |
| **Terminals and streams** | a `tty` or a `stream` drawn live, in colour. One you may type into is resized for the phone, with the keys a phone keyboard lacks |
| **Links** | send one to a machine's `link` namespace and it opens over there |

What arrives lands in `Download/drop/`, where the phone's own file manager finds it.

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
it as an artifact on every push. It is signed with the debug key, so it installs — but it is not a
release signature, and two builds signed that way are not upgradeable over each other.
