# drop on Android

The whole node, on a phone. The Go core is compiled to a native library and bound for the JVM; the
Kotlin around it is a screen and a service, and nothing else.

```
  apps/android/
    mobile/     Go — the binding gomobile turns into an AAR
    app/        Kotlin — one activity, one foreground service
    libs/       where the built AAR lands (not in git)
```

## Why it is shaped this way

Everything drop knows how to do is already in Go, and it compiles for `android/arm64` unchanged —
iroh, QUIC, the archetypes, the lot. Rewriting any of that for the phone would mean two
implementations of one protocol, and they would drift.

So the phone runs the same code, and Kotlin does only what Android will not let Go do: ask for a
notification permission, put a foreground service in the tray, and draw a screen. There is no Java
anywhere.

`mobile/mobile.go` is the whole surface between them. gomobile binds a narrow set of types, so
everything crossing is a string, an int64, a bool, or an interface declared there — lists cross as
one entry per line.

## Building it

Needs the Android shell, which carries a pinned SDK, NDK, gomobile, JDK and Gradle:

```console
nix develop .#android
make aar          # the Go core → apps/android/libs/mobile.aar
make apk          # the AAR + Kotlin → an installable, signed APK
make install-apk  # onto a plugged-in device
```

`make apk` depends on `make aar`, so the second command alone is enough.

## What it does so far

Starts a node, keeps it reachable while the app is in the background, and shows what this device is
and where it can be reached. The address book is readable from the binding; pairing, sending and
the archetypes are not bound yet.

## What Android takes away

| | |
|---|---|
| **Background execution** | a node nobody can reach is not a node, so it runs as a foreground service — which costs a permanent notification. There is no way around that on modern Android. |
| **Storage** | everything lives under the app's private directory, handed to the Go side at startup as `XDG_CONFIG_HOME` and `XDG_DATA_HOME`. Uninstalling takes the identity with it. |
| **Identity** | a phone has no readable board or drive serial, so the key is generated once and written down rather than derived from the hardware. See [identity](../../docs/identity.md). |
| **Doze** | the OS may still stop the process when the screen has been off a long time. The service restarts, and the node comes back as itself. |
| **Its own addresses** | Android refuses an ordinary app the netlink socket Go asks for to list interfaces, so the node cannot see the addresses it has. It publishes its relay and nothing else, which means a phone and a laptop on one wire still meet through a relay in another country rather than over the wire. |

The last one is visible in `logcat` as a repeating `avc: denied { bind } ... netlink_route_socket`
(`b/155595000`). It is a platform restriction rather than a bug here, and the way out is to ask
Android for the addresses through `ConnectivityManager` in Kotlin and hand them to the Go side —
which is not done yet.

## The APK from CI

The `android` job in `.github/workflows/release.yml` builds it in the same flake shell and uploads
it as an artifact on every push. It is signed with the debug key, so it installs — but it is not a
release signature, and two builds signed that way are not upgradeable over each other.
