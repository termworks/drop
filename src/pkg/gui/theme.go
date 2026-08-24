//go:build js || android

package gui

import (
	"image/color"

	"gioui.org/text"
	"gioui.org/unit"
	"gioui.org/widget/material"
)

// The palette is Radix Colors' dark scales, not invented here.
//
// Radix publishes twelve steps per hue and says what each one is for: 1 is the page, 2 a raised
// surface, 3 a component at rest, 4 and 5 hover and press, 6 a separator, 7 a border, 9 the solid
// accent, 11 text that should recede, 12 text that should not. Following somebody's calibration
// beats guessing at a dozen hex values that only have to work against each other.
//
// mauve is the neutral — grey with violet in it, which is why the chrome sits under a violet accent
// without arguing with it. violet and pink are the brand.
var (
	// mauve: everything that is not an accent.
	ground = color.NRGBA{R: 0x12, G: 0x11, B: 0x13, A: 0xff} // 1  the page
	panel  = color.NRGBA{R: 0x23, G: 0x22, B: 0x25, A: 0xff} // 3  a card, a component at rest
	wash   = color.NRGBA{R: 0x1a, G: 0x19, B: 0x1b, A: 0xff} // 2  a quiet button, a field on the page
	press  = color.NRGBA{R: 0x32, G: 0x30, B: 0x35, A: 0xff} // 5  held down
	line   = color.NRGBA{R: 0x3c, G: 0x39, B: 0x3f, A: 0xff} // 6  a separator
	edge   = color.NRGBA{R: 0x49, G: 0x47, B: 0x4e, A: 0xff} // 7  the edge of a card
	faint  = color.NRGBA{R: 0x7c, G: 0x7a, B: 0x85, A: 0xff} // 10 a hint, a placeholder
	dim    = color.NRGBA{R: 0xb5, G: 0xb2, B: 0xbc, A: 0xff} // 11 text that should recede
	ink    = color.NRGBA{R: 0xee, G: 0xee, B: 0xf0, A: 0xff} // 12 text that should not

	// violet and pink: the brand, and the two ends of every gradient in the interface.
	violet   = color.NRGBA{R: 0x6e, G: 0x56, B: 0xcf, A: 0xff} // 9  solid
	violetUp = color.NRGBA{R: 0x7d, G: 0x66, B: 0xd9, A: 0xff} // 10 solid, hovered
	violetOn = color.NRGBA{R: 0xba, G: 0xa7, B: 0xff, A: 0xff} // 11 accent text on a dark ground
	plum     = color.NRGBA{R: 0xd6, G: 0x40, B: 0x9f, A: 0xff} // pink 9
	deep     = color.NRGBA{R: 0x33, G: 0x25, B: 0x5b, A: 0xff} // violet 4

	// grass and ruby, for the two things that are not brand: it worked, it did not.
	good = color.NRGBA{R: 0x71, G: 0xd0, B: 0x83, A: 0xff} // grass 11
	bad  = color.NRGBA{R: 0xff, G: 0x94, B: 0x9d, A: 0xff} // ruby 11

	onDark = color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff}
	shadow = color.NRGBA{A: 0x66}
	sheet  = color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff}
	black  = color.NRGBA{R: 0x0b, G: 0x0a, B: 0x10, A: 0xff}
)

// One spacing scale, on a four point grid, so nothing invents its own.
const (
	hair   = unit.Dp(1)
	tight  = unit.Dp(4)
	gap    = unit.Dp(8)
	pad    = unit.Dp(16)
	roomy  = unit.Dp(24)
	wide   = unit.Dp(32)
	round  = unit.Dp(12)
	bigger = unit.Dp(20)
	pill   = unit.Dp(999)
	touch  = unit.Dp(48)
	stripe = unit.Dp(3)
)

// One type scale. Six sizes is enough for three screens, and fewer than the eleven a toolkit will
// hand you if you let it.
const (
	sizeTiny    = unit.Sp(12)
	sizeSmall   = unit.Sp(13)
	sizeBody    = unit.Sp(15)
	sizeStrong  = unit.Sp(17)
	sizeTitle   = unit.Sp(21)
	sizeDisplay = unit.Sp(28)
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
