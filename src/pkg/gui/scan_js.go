//go:build js

package gui

import (
	"errors"
	"image"
	"syscall/js"
)

// Reading a code in a browser: the camera through getUserMedia, the decoding through the platform's
// own BarcodeDetector.
//
// The preview is a real <video> laid over the canvas rather than frames copied into Gio. A phone
// decodes video in hardware and hands it straight to the compositor; pulling every frame through
// WebAssembly to draw it again would cost more than the scanning does.

var live struct {
	overlay js.Value
	stream  js.Value
	timer   js.Value
	held    []js.Func
}

// canScan is about the camera, not the decoding: the decoder is Go and always there.
func canScan() bool {
	media := js.Global().Get("navigator").Get("mediaDevices")
	return media.Truthy() && media.Get("getUserMedia").Truthy()
}

func startScan(to scanning) error {
	if !canScan() {
		return errors.New("this browser cannot read a code with the camera")
	}
	stopScan()

	doc := js.Global().Get("document")

	overlay := doc.Call("createElement", "div")
	overlay.Get("style").Set("cssText", `
		position:fixed; inset:0; z-index:2147483647; background:#0e0b14;
		display:flex; flex-direction:column; align-items:stretch;`)

	video := doc.Call("createElement", "video")
	video.Set("autoplay", true)
	video.Set("muted", true)
	video.Set("playsInline", true)
	video.Call("setAttribute", "playsinline", "true")
	video.Get("style").Set("cssText", "flex:1; width:100%; min-height:0; object-fit:cover;")

	said := doc.Call("createElement", "div")
	said.Set("textContent", "Point the camera at the code on the other device")
	said.Get("style").Set("cssText", `
		font:15px/1.5 system-ui,sans-serif; color:#aba1bd; text-align:center; padding:18px 18px 6px;`)

	stop := doc.Call("createElement", "button")
	stop.Set("textContent", "Cancel")
	stop.Get("style").Set("cssText", `
		margin:10px 18px 28px; padding:14px; border:0; border-radius:14px;
		background:#231a34; color:#c9a6ff; font:bold 15px system-ui,sans-serif;`)

	overlay.Call("appendChild", said)
	overlay.Call("appendChild", video)
	overlay.Call("appendChild", stop)
	doc.Get("body").Call("appendChild", overlay)

	live.overlay = overlay

	cancelled := js.FuncOf(func(js.Value, []js.Value) any {
		stopScan()
		to.failed(errors.New("scanning stopped"))
		return nil
	})
	live.held = append(live.held, cancelled)
	stop.Call("addEventListener", "click", cancelled)

	// The rear camera when there is one to ask for: a phone held up to another screen is not
	// looking at its owner.
	want := map[string]any{
		"video": map[string]any{"facingMode": map[string]any{"ideal": "environment"}},
		"audio": false,
	}

	got := js.Global().Get("navigator").Get("mediaDevices").Call("getUserMedia", want)
	then(got,
		func(stream js.Value) {
			live.stream = stream
			video.Set("srcObject", stream)
			watchFor(video, to)
		},
		func(err js.Value) {
			stopScan()
			to.failed(errors.New(whyNot(err, "the camera could not be opened")))
		},
	)
	return nil
}

// watchFor looks at the preview a few times a second until it finds a code.
//
// Polling rather than every frame: decoding runs over the whole image, and doing that sixty times a
// second heats a phone for no gain when a code held up to a camera stays there for seconds.
func watchFor(video js.Value, to scanning) {
	doc := js.Global().Get("document")
	sheet := doc.Call("createElement", "canvas")

	var look js.Func
	look = js.FuncOf(func(js.Value, []js.Value) any {
		if !live.overlay.Truthy() {
			return nil
		}

		frame, ok := grab(sheet, video)
		if !ok {
			return nil
		}

		text, err := readCode(frame)
		if err != nil || text == "" {
			return nil // a frame with no code in it is just a frame; the next one may have one
		}

		stopScan()
		to.found(text)
		return nil
	})

	live.held = append(live.held, look)
	live.timer = js.Global().Call("setInterval", look, 400)
}

// grab copies the current video frame into an image Go can read.
//
// Scaled down on the way: a decoder wants enough pixels to resolve the smallest square in the code
// and no more, and a phone's camera hands over far more than that. Copying four megapixels across
// the WebAssembly boundary several times a second is the one thing here that would be slow.
func grab(sheet, video js.Value) (image.Image, bool) {
	w := video.Get("videoWidth").Int()
	h := video.Get("videoHeight").Int()
	if w == 0 || h == 0 {
		return nil, false
	}

	const most = 640
	if w > most || h > most {
		if w > h {
			h, w = h*most/w, most
		} else {
			w, h = w*most/h, most
		}
	}

	sheet.Set("width", w)
	sheet.Set("height", h)

	ctx := sheet.Call("getContext", "2d", map[string]any{"willReadFrequently": true})
	if !ctx.Truthy() {
		return nil, false
	}
	ctx.Call("drawImage", video, 0, 0, w, h)

	data := ctx.Call("getImageData", 0, 0, w, h).Get("data")

	frame := image.NewRGBA(image.Rect(0, 0, w, h))
	js.CopyBytesToGo(frame.Pix, data)

	return frame, true
}

// stopScan takes the camera and the overlay away. Safe to call when nothing is running, which is
// what lets every path out of scanning call it without checking first.
func stopScan() {
	if live.timer.Truthy() {
		js.Global().Call("clearInterval", live.timer)
		live.timer = js.Value{}
	}

	if live.stream.Truthy() {
		tracks := live.stream.Call("getTracks")
		for i := range tracks.Length() {
			tracks.Index(i).Call("stop")
		}
		live.stream = js.Value{}
	}

	if live.overlay.Truthy() {
		live.overlay.Call("remove")
		live.overlay = js.Value{}
	}

	for _, fn := range live.held {
		fn.Release()
	}
	live.held = nil
}

// then attaches Go to a JavaScript promise.
func then(promise js.Value, ok func(js.Value), bad func(js.Value)) {
	var done, failed js.Func

	done = js.FuncOf(func(_ js.Value, args []js.Value) any {
		var value js.Value
		if len(args) > 0 {
			value = args[0]
		}
		ok(value)
		return nil
	})
	failed = js.FuncOf(func(_ js.Value, args []js.Value) any {
		var value js.Value
		if len(args) > 0 {
			value = args[0]
		}
		bad(value)
		return nil
	})

	live.held = append(live.held, done, failed)
	promise.Call("then", done).Call("catch", failed)
}

// whyNot reads whatever a browser put in an error, and falls back to something a person can act on.
func whyNot(err js.Value, instead string) string {
	if err.Truthy() {
		if message := err.Get("message"); message.Truthy() && message.String() != "" {
			return message.String()
		}
		if name := err.Get("name"); name.Truthy() && name.String() != "" {
			return name.String()
		}
	}
	return instead
}

// scanFrame is nothing in a browser: the preview is a video element the page composites itself,
// rather than pixels this has to carry.
func scanFrame() image.Image { return nil }
