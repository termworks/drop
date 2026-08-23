//go:build js || android

package gui

import (
	"image"
	"math"

	"gioui.org/f32"
	"gioui.org/layout"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
)

// drop's mark: a sphere of nodes with the lines between them.
//
// The logo is a faceted globe — a mesh of peers, which is what drop is. Gio draws shapes rather than
// files, so this is that idea in the fewest strokes: two rings of nodes, the lines that join them,
// and the gradient the rest of the interface uses.
func (a *App) mark(gtx layout.Context, size int) layout.Dimensions {
	if size < 8 {
		size = 8
	}

	middle := f32.Pt(float32(size)/2, float32(size)/2)
	radius := float32(size) / 2
	weight := float32(gtx.Dp(unit.Dp(1))) * float32(size) / 88

	// Two rings, offset from each other, so the lines between them cross the face the way the
	// facets on the logo do.
	outer := ringOf(middle, radius*0.86, 9, 0)
	inner := ringOf(middle, radius*0.44, 6, math.Pi/6)

	paint.ColorOp{Color: mix(violet, plum, 0.5)}.Add(gtx.Ops)

	// The rim first, then the web across it.
	stroke(gtx, ring(middle, radius*0.86), weight)
	for i, at := range outer {
		stroke(gtx, segment(at, outer[(i+1)%len(outer)]), weight)
		stroke(gtx, segment(at, inner[i%len(inner)]), weight)
	}
	for i, at := range inner {
		stroke(gtx, segment(at, inner[(i+1)%len(inner)]), weight)
	}

	// The nodes themselves, filled, so the mesh reads as points joined rather than a scribble.
	for _, at := range append(outer, inner...) {
		disc(gtx, at, weight*2)
	}
	disc(gtx, middle, weight*2.4)

	return layout.Dimensions{Size: image.Pt(size, size)}
}

func ringOf(middle f32.Point, radius float32, count int, turn float64) []f32.Point {
	out := make([]f32.Point, 0, count)

	for i := range count {
		angle := turn + 2*math.Pi*float64(i)/float64(count)
		out = append(out, f32.Pt(
			middle.X+radius*float32(math.Cos(angle)),
			middle.Y+radius*float32(math.Sin(angle)),
		))
	}
	return out
}

// stroke draws one path at a weight, in whatever colour was last set.
func stroke(gtx layout.Context, shape func(*clip.Path), weight float32) {
	var p clip.Path
	p.Begin(gtx.Ops)
	shape(&p)

	outline := clip.Stroke{Path: p.End(), Width: weight}.Op()
	defer outline.Push(gtx.Ops).Pop()

	paint.PaintOp{}.Add(gtx.Ops)
}

func ring(middle f32.Point, radius float32) func(*clip.Path) {
	return func(p *clip.Path) {
		p.MoveTo(f32.Pt(middle.X+radius, middle.Y))
		p.ArcTo(middle, middle, 2*math.Pi)
	}
}

func segment(from, to f32.Point) func(*clip.Path) {
	return func(p *clip.Path) {
		p.MoveTo(from)
		p.LineTo(to)
	}
}

// disc is one node of the mesh.
func disc(gtx layout.Context, at f32.Point, radius float32) {
	shape := clip.Ellipse{
		Min: image.Pt(int(at.X-radius), int(at.Y-radius)),
		Max: int2(at.X+radius, at.Y+radius),
	}
	defer shape.Push(gtx.Ops).Pop()
	paint.PaintOp{}.Add(gtx.Ops)
}

func int2(x, y float32) image.Point { return image.Pt(int(x), int(y)) }
