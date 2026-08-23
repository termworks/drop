//go:build js || android

package gui

import (
	"image/color"

	"gioui.org/text"
	"gioui.org/unit"
	"gioui.org/widget/material"
)

// The palette. One violet, one plum, and neutrals biased toward them — a grey mixed from the accent
// reads as chosen, where a pure grey reads as whatever the toolkit had lying around.
var (
	violet = color.NRGBA{R: 0x4c, G: 0x1d, B: 0x95, A: 0xff}
	plum   = color.NRGBA{R: 0xc2, G: 0x40, B: 0x8f, A: 0xff}
	deep   = color.NRGBA{R: 0x35, G: 0x14, B: 0x6b, A: 0xff}
	ink    = color.NRGBA{R: 0x1c, G: 0x18, B: 0x24, A: 0xff}
	dim    = color.NRGBA{R: 0x6b, G: 0x63, B: 0x78, A: 0xff}
	faint  = color.NRGBA{R: 0x9c, G: 0x94, B: 0xa8, A: 0xff}
	line   = color.NRGBA{R: 0xe6, G: 0xe1, B: 0xec, A: 0xff}
	panel  = color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff}
	ground = color.NRGBA{R: 0xf7, G: 0xf5, B: 0xfa, A: 0xff}
	wash   = color.NRGBA{R: 0xf1, G: 0xec, B: 0xf8, A: 0xff}
	onDark = color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff}
	good   = color.NRGBA{R: 0x2f, G: 0x6f, B: 0x4f, A: 0xff}
	bad    = color.NRGBA{R: 0xa3, G: 0x32, B: 0x4f, A: 0xff}
	shadow = color.NRGBA{R: 0x2a, G: 0x1c, B: 0x3d, A: 0x0e}
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
