//go:build js || android

package gui

import (
	"image"
	"image/color"

	"gioui.org/f32"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
)

// card is a raised surface: a soft shadow, then the panel, then whatever sits on it.
//
// The shadow is what separates a list of things from a wall of text. It is barely there on purpose —
// enough to say "this is a thing you can press", not enough to be a drop shadow anybody notices.
func card(gtx layout.Context, radius unit.Dp, w layout.Widget) layout.Dimensions {
	macro := op.Record(gtx.Ops)
	dims := w(gtx)
	call := macro.Stop()

	lift := gtx.Dp(unit.Dp(2))
	under := image.Rectangle{
		Min: image.Pt(0, lift),
		Max: image.Pt(dims.Size.X, dims.Size.Y+lift),
	}
	rounded(gtx, under, radius, shadow)

	rounded(gtx, image.Rectangle{Max: dims.Size}, radius, panel)
	call.Add(gtx.Ops)

	return dims
}

// rounded paints one rounded rectangle.
func rounded(gtx layout.Context, at image.Rectangle, radius unit.Dp, c color.NRGBA) {
	r := gtx.Dp(radius)
	if max := min(at.Dx(), at.Dy()) / 2; r > max {
		r = max
	}

	shape := clip.RRect{Rect: at, SE: r, SW: r, NE: r, NW: r}
	defer shape.Push(gtx.Ops).Pop()
	paint.Fill(gtx.Ops, c)
}

// ramp paints the brand gradient across a shape, which is the one flourish this interface allows
// itself: it marks what is *this device acting*, and nothing else uses it.
func ramp(gtx layout.Context, at image.Rectangle, radius unit.Dp) {
	r := gtx.Dp(radius)
	if max := min(at.Dx(), at.Dy()) / 2; r > max {
		r = max
	}

	shape := clip.RRect{Rect: at, SE: r, SW: r, NE: r, NW: r}
	defer shape.Push(gtx.Ops).Pop()

	paint.LinearGradientOp{
		Stop1:  f32.Pt(float32(at.Min.X), float32(at.Min.Y)),
		Stop2:  f32.Pt(float32(at.Max.X), float32(at.Max.Y)),
		Color1: violet,
		Color2: plum,
	}.Add(gtx.Ops)
	paint.PaintOp{}.Add(gtx.Ops)
}

// filled draws a widget on the brand gradient.
func filled(gtx layout.Context, radius unit.Dp, w layout.Widget) layout.Dimensions {
	macro := op.Record(gtx.Ops)
	dims := w(gtx)
	call := macro.Stop()

	ramp(gtx, image.Rectangle{Max: dims.Size}, radius)
	call.Add(gtx.Ops)

	return dims
}

// tinted draws a widget on a flat colour.
func tinted(gtx layout.Context, c color.NRGBA, radius unit.Dp, w layout.Widget) layout.Dimensions {
	macro := op.Record(gtx.Ops)
	dims := w(gtx)
	call := macro.Stop()

	rounded(gtx, image.Rectangle{Max: dims.Size}, radius, c)
	call.Add(gtx.Ops)

	return dims
}

// rule is a hairline, for where a border says more than a gap.
func rule(gtx layout.Context) layout.Dimensions {
	height := gtx.Dp(unit.Dp(1))
	at := image.Rect(0, 0, gtx.Constraints.Max.X, height)

	defer clip.Rect(at).Push(gtx.Ops).Pop()
	paint.Fill(gtx.Ops, line)

	return layout.Dimensions{Size: at.Max}
}
