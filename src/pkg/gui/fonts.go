//go:build js || android

package gui

import (
	"gioui.org/font"
	"gioui.org/font/opentype"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/gofont/gomono"
	"golang.org/x/image/font/gofont/goregular"
)

// faces is the type this interface draws with.
//
// Three faces rather than the whole family. Gio's own collection registers eleven — italics, medium
// weights, small caps — and every one of them is a font file compiled into the binary. In a browser
// that is a megabyte a person waits for before seeing anything, to render text that never asks for
// them.
func faces() []font.FontFace {
	var out []font.FontFace

	for _, at := range []struct {
		data []byte
		desc font.Font
	}{
		{goregular.TTF, font.Font{Typeface: "Go"}},
		{gobold.TTF, font.Font{Typeface: "Go", Weight: font.Bold}},
		{gomono.TTF, font.Font{Typeface: "Go Mono"}},
	} {
		face, err := opentype.Parse(at.data)
		if err != nil {
			continue
		}
		out = append(out, font.FontFace{Font: at.desc, Face: face})
	}
	return out
}
