//go:build js || android

package gui

// Scanning is a camera and a decoder, and neither is Gio's business: it draws. So the interface
// asks the platform underneath — a browser has getUserMedia and a barcode detector, a phone has a
// camera app's worth of Android — and both answer through this pair of functions.
//
// found is called with the text of whatever was read; failed with the reason nothing was. Exactly
// one of them fires, on some goroutine, and the interface is redrawn either way.
type scanning struct {
	found  func(string)
	failed func(error)
}
