# The graphical interfaces, and what to do about them later

Written after scrapping the web and phone interfaces. Nothing here is being worked on now: the
binaries and the terminal interface come first, so that when something does not work there is one
place it can be wrong instead of three.

## Why they were scrapped

A phone scanned a pairing code and then timed out. Nobody could say whether that was the interface,
the transport, or the pairing protocol, because all three were new at once. That is the whole
argument: an interface built on a protocol nobody has exercised is a way of not finding out which
one is broken.

## What was actually built, so it is not rediscovered

- **Gio for both** — one Go interface compiled to WebAssembly for the browser and to an `.aar` for
  Android. `src/pkg/gui/`, entry points in `browser/` and `phone/`.
- **A dark palette taken from Radix Colors**, not invented. Twelve steps per hue, each with a
  documented job: 1 the page, 3 a component at rest, 5 pressed, 6 a separator, 7 a border, 9 the
  solid accent, 11 text that recedes, 12 text that does not. `mauve` for chrome, `violet` and `pink`
  for the brand, `grass`/`ruby` for worked and did not. Worth keeping whatever the toolkit is.
- **Camera scanning that worked in a browser** — the camera through `getUserMedia`, the decoding in
  Go with `github.com/liyue201/goqr`, because `BarcodeDetector` does not exist outside Android and
  ChromeOS. Verified headlessly by feeding Chrome a Y4M video of a real ticket's code and watching
  the pairing complete.
- **Camera scanning on Android with no Java at all** — NDK Camera2 (`ACameraManager`, `AImageReader`)
  through cgo, the permission requested over JNI against the Activity from `app.ViewEvent`. It
  compiled and linked. Whether it ever opened a camera on a real phone was never established.

All of it is in the history. `git log --oneline` around the commits named `feat(ui)` and
`feat(android)`.

## The question to answer before starting again: Gio, or Kotlin over a Go core?

The way most Go projects do Android is not Gio. It is **`gomobile bind`**: the Go code is compiled
into an `.aar` as a *library*, `gobind` generates Java bindings for every exported symbol, and the
interface is ordinary Kotlin — today, Compose. The Go side runs on a background thread and hands
results to the UI thread through `runOnUiThread`.

| | Gio | gomobile bind + Compose |
|---|---|---|
| Interface code | Go, shared with the web | Kotlin, Android only |
| Looks like an Android app | no, it is a canvas | yes |
| Camera, share sheet, notifications | cgo and JNI by hand | the platform API, directly |
| Web interface | the same code | needs a separate one |
| What breaks | drawing, input, fonts | the boundary between the two |

The honest reading: Gio bought one interface for two places and made every platform feature — a
camera, a share sheet, an icon — a piece of systems work. Compose costs a second interface and gives
all of that back. If Android matters more than the browser, `gomobile bind` is the road most
travelled and the one with answers on it.

Decide that before writing another screen.

## When it is time

1. The protocol is exercised from the terminal first: pair, chat, files, links, a terminal, over LAN
   and over a relay, between two machines that are not this one.
2. Then one interface, for whichever platform is worth it, over an API that already works.
3. Keep the palette. Keep the logo. Keep `src/pkg/ticket` and the Go QR decoding, which are not
   interface code and were never the problem.

## Sources

- gomobile: <https://pkg.go.dev/golang.org/x/mobile/cmd/gomobile>, <https://go.dev/wiki/Mobile>
- a worked Kotlin + Go project: <https://github.com/wilyarti/android-kotlin-golang-example-project>
- Radix Colors scale semantics: <https://www.radix-ui.com/colors/docs/palette-composition/understanding-the-scale>
