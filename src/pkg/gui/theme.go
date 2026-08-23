//go:build js || android

package gui

import (
	"image/color"

	"gioui.org/text"
	"gioui.org/unit"
	"gioui.org/widget/material"
)

// The same violet the rest of drop uses, so a window, a phone and a browser tab are recognisably one
// program.
var (
	violet = color.NRGBA{R: 0x4c, G: 0x1d, B: 0x95, A: 0xff}
	plum   = color.NRGBA{R: 0x6b, G: 0x22, B: 0x73, A: 0xff}
	ink    = color.NRGBA{R: 0x1f, G: 0x1b, B: 0x26, A: 0xff}
	dim    = color.NRGBA{R: 0x6b, G: 0x63, B: 0x76, A: 0xff}
	faint  = color.NRGBA{R: 0xa4, G: 0x9d, B: 0xae, A: 0xff}
	line   = color.NRGBA{R: 0xe5, G: 0xe1, B: 0xea, A: 0xff}
	panel  = color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff}
	ground = color.NRGBA{R: 0xfa, G: 0xf9, B: 0xfb, A: 0xff}
	onDark = color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff}
	bad    = color.NRGBA{R: 0xa3, G: 0x32, B: 0x4f, A: 0xff}
	soft   = color.NRGBA{R: 0xf2, G: 0xef, B: 0xf6, A: 0xff}
)

// Spacing, one scale, so nothing invents its own.
const (
	gap   = unit.Dp(8)
	pad   = unit.Dp(16)
	round = unit.Dp(12)
	touch = unit.Dp(48)
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
