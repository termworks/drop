//go:build js || android

package gui

import (
	"image/color"

	"gioui.org/text"
	"gioui.org/unit"
	"gioui.org/widget/material"
)

// The palette: dark, with neutrals mixed from the accent rather than pure greys.
//
// One theme, not two. drop is a thing you glance at in the evening to send a file, and a page that
// flashes white to do it is worse than one that commits. Every neutral here carries a little of the
// violet, which is what keeps a dark interface from looking like switched-off glass.
var (
	violet = color.NRGBA{R: 0x7c, G: 0x3a, B: 0xed, A: 0xff}
	plum   = color.NRGBA{R: 0xe0, G: 0x45, B: 0x9b, A: 0xff}
	deep   = color.NRGBA{R: 0x4c, G: 0x1d, B: 0x95, A: 0xff}
	ink    = color.NRGBA{R: 0xf3, G: 0xef, B: 0xf9, A: 0xff}
	dim    = color.NRGBA{R: 0xab, G: 0xa1, B: 0xbd, A: 0xff}
	faint  = color.NRGBA{R: 0x73, G: 0x69, B: 0x88, A: 0xff}
	line   = color.NRGBA{R: 0x27, G: 0x1e, B: 0x36, A: 0xff}
	edge   = color.NRGBA{R: 0x34, G: 0x29, B: 0x47, A: 0xff}
	panel  = color.NRGBA{R: 0x18, G: 0x13, B: 0x23, A: 0xff}
	ground = color.NRGBA{R: 0x0e, G: 0x0b, B: 0x14, A: 0xff}
	wash   = color.NRGBA{R: 0x23, G: 0x1a, B: 0x34, A: 0xff}
	onDark = color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff}
	good   = color.NRGBA{R: 0x45, G: 0xd4, B: 0x92, A: 0xff}
	bad    = color.NRGBA{R: 0xff, G: 0x76, B: 0x8b, A: 0xff}
	shadow = color.NRGBA{A: 0x59}
	sheet  = color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff}
	black  = color.NRGBA{R: 0x0b, G: 0x0a, B: 0x10, A: 0xff}
)

// One spacing scale, so nothing invents its own.
const (
	tight  = unit.Dp(4)
	gap    = unit.Dp(10)
	pad    = unit.Dp(18)
	roomy  = unit.Dp(26)
	round  = unit.Dp(14)
	pill   = unit.Dp(999)
	touch  = unit.Dp(48)
	stripe = unit.Dp(3)
)

func newTheme() *material.Theme {
	th := material.NewTheme()
	th.Shaper = text.NewShaper(text.WithCollection(faces()))
	th.Palette.Bg = ground
	th.Palette.Fg = ink
	th.Palette.ContrastBg = violet
	th.Palette.ContrastFg = onDark
	return th
}

// mix blends two colours, for the gradient that carries the brand across a surface.
func mix(a, b color.NRGBA, at float32) color.NRGBA {
	if at < 0 {
		at = 0
	}
	if at > 1 {
		at = 1
	}
	return color.NRGBA{
		R: uint8(float32(a.R) + (float32(b.R)-float32(a.R))*at),
		G: uint8(float32(a.G) + (float32(b.G)-float32(a.G))*at),
		B: uint8(float32(a.B) + (float32(b.B)-float32(a.B))*at),
		A: 0xff,
	}
}
