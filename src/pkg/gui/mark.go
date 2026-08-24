//go:build js || android

package gui

import (
	"bytes"
	_ "embed"
	"image"
	"image/png"
	"sync"

	"gioui.org/f32"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
)

// drop's mark: the faceted sphere, which is the same drawing the favicon and the phone's icon use.
//
// It is baked to a bitmap rather than drawn with Gio's path operations. The logo is sixteen
// kilobytes of SVG path with elliptical arcs in it, and an approximation of it in strokes is a
// different mark, not a cheaper one.
//
//go:embed mark.png
var markPNG []byte

var markImage = sync.OnceValue(func() paint.ImageOp {
	drawn, err := png.Decode(bytes.NewReader(markPNG))
	if err != nil {
		return paint.ImageOp{}
	}
	return paint.NewImageOp(drawn)
})

func (a *App) mark(gtx layout.Context, size int) layout.Dimensions {
	if size < 8 {
		size = 8
	}

	drawn := markImage()
	at := drawn.Size()
	if at.X == 0 || at.Y == 0 {
		return layout.Dimensions{Size: image.Pt(size, size)}
	}

	// Scaled about the origin, then clipped to the square it was asked for: the bitmap is drawn
	// at one size and used at several, and an unclipped image op paints past what it returns.
	defer op.Affine(scaleTo(at, size)).Push(gtx.Ops).Pop()
	defer clip.Rect{Max: at}.Push(gtx.Ops).Pop()

	drawn.Add(gtx.Ops)
	paint.PaintOp{}.Add(gtx.Ops)

	return layout.Dimensions{Size: image.Pt(size, size)}
}

func scaleTo(from image.Point, to int) f32.Affine2D {
	by := float32(to) / float32(from.X)
	return f32.Affine2D{}.Scale(f32.Pt(0, 0), f32.Pt(by, by))
}
