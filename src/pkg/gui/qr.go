//go:build js || android

package gui

import (
	"image"
	"image/color"

	"gioui.org/layout"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"rsc.io/qr"
)

// qrCode draws a code as squares rather than as text.
//
// Drawing it with characters means a module is however wide the font makes it and however tall the
// line is, and those two are not the same number — so the code comes out stretched one way or the
// other, and a camera gives up on it. Rectangles have no such opinion.
func qrCode(gtx layout.Context, code *qr.Code, ink color.NRGBA) layout.Dimensions {
	const quiet = 2

	across := code.Size + quiet*2

	// One module is a whole number of pixels, so every one is the same size and the edges stay
	// sharp. A fractional module would blur into its neighbour at exactly the scale a reader cares
	// about.
	side := min(gtx.Constraints.Max.X, gtx.Constraints.Max.Y) / across
	if side < 1 {
		side = 1
	}
	full := side * across

	for y := range code.Size {
		for x := range code.Size {
			if !code.Black(x, y) {
				continue
			}

			at := image.Rect(
				(x+quiet)*side, (y+quiet)*side,
				(x+quiet+1)*side, (y+quiet+1)*side,
			)
			stack := clip.Rect(at).Push(gtx.Ops)
			paint.Fill(gtx.Ops, ink)
			stack.Pop()
		}
	}

	return layout.Dimensions{Size: image.Pt(full, full)}
}
