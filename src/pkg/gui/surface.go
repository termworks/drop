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

// card is a raised surface: a shadow under it, a hairline around it, then the panel.
//
// On a dark ground a shadow alone does almost nothing — dark on dark is dark. What separates one
// card from the page is the hairline: a single pixel of something lighter than the panel, which is
// how a raised edge catches light in the real world.
func card(gtx layout.Context, radius unit.Dp, w layout.Widget) layout.Dimensions {
	return raised(gtx, radius, panel, w)
}

// raised is card with the surface colour named, for the few places that want something other than
// the panel — a code on white, because a camera reads contrast and not intent.
func raised(gtx layout.Context, radius unit.Dp, surface color.NRGBA, w layout.Widget) layout.Dimensions {
	macro := op.Record(gtx.Ops)
	dims := w(gtx)
	call := macro.Stop()

	lift := gtx.Dp(unit.Dp(3))
	under := image.Rectangle{
		Min: image.Pt(0, lift),
		Max: image.Pt(dims.Size.X, dims.Size.Y+lift),
	}
	rounded(gtx, under, radius, shadow)

	whole := image.Rectangle{Max: dims.Size}
	rounded(gtx, whole, radius, edge)

	hair := gtx.Dp(unit.Dp(1))
	inside := image.Rectangle{
		Min: image.Pt(hair, hair),
		Max: image.Pt(dims.Size.X-hair, dims.Size.Y-hair),
	}
	rounded(gtx, inside, radius, surface)

	call.Add(gtx.Ops)
	return dims
}

// glow is a soft pool of colour behind something, for the one place the page wants depth rather
// than another box.
//
// Gio has no radial gradient and no blur, so it is many circles at a very low alpha: few and strong
// reads as concentric rings, many and weak reads as light.
func glow(gtx layout.Context, at image.Rectangle, c color.NRGBA, spread unit.Dp) {
	const layers = 18

	step := gtx.Dp(spread) / layers
	if step < 1 {
		step = 1
	}

	for i := layers; i > 0; i-- {
		grow := step * i
		ring := image.Rectangle{
			Min: image.Pt(at.Min.X-grow, at.Min.Y-grow),
			Max: image.Pt(at.Max.X+grow, at.Max.Y+grow),
		}
		soft := c
		soft.A = 5
		rounded(gtx, ring, unit.Dp(999), soft)
	}
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
