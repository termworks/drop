//go:build js || android

package gui

import (
	"gioui.org/font"
	"gioui.org/layout"
	"gioui.org/unit"
	"strings"

	"gioui.org/widget"
	"gioui.org/widget/material"
)

// pairing is the two halves of linking a device: showing a code, and reading one.
//
// Both, because pairing has two sides and an interface that could only do the first would leave
// whoever is holding the other device with nowhere to type what they are looking at.
func (a *App) pairing(gtx layout.Context, at *linking) layout.Dimensions {
	if a.pairStop.Clicked(gtx) {
		a.stopPairing()
	}
	if a.joinGo.Clicked(gtx) {
		a.joinWhatIsTyped()
	}
	for {
		event, ok := a.joinField.Update(gtx)
		if !ok {
			break
		}
		if _, sent := event.(widget.SubmitEvent); sent {
			a.joinWhatIsTyped()
		}
	}

	// The whole screen scrolls: the code takes real room and the box for reading theirs sits
	// under it, and a control you cannot reach is one that is not there.
	a.pairScroll.Axis = layout.Vertical

	return a.pairScroll.Layout(gtx, 1, func(gtx layout.Context, _ int) layout.Dimensions {
		return layout.Inset{Top: gap, Left: pad, Right: pad, Bottom: pad}.Layout(gtx,
			func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						if at.err == "" {
							return layout.Dimensions{}
						}
						return layout.Inset{Bottom: gap}.Layout(gtx, a.small(at.err, bad).Layout)
					}),

					// The code, on a white card so a camera has the contrast it wants whatever the
					// surrounding page is doing.
					// Given room rather than whatever is left over. A code shrunk to fit is one a camera
					// cannot resolve, and the rest of this screen can scroll instead.
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						if at.code == nil {
							return layout.Dimensions{}
						}
						// A third of the shorter side, so it is generous on a phone held upright and does not
						// swallow a wide window.
						want := min(gtx.Constraints.Max.X, gtx.Constraints.Max.Y*2) * 2 / 5
						if want < gtx.Dp(unit.Dp(180)) {
							want = gtx.Dp(unit.Dp(180))
						}
						gtx.Constraints.Max.X = want

						return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
							return raised(gtx, round, sheet, func(gtx layout.Context) layout.Dimensions {
								return layout.UniformInset(pad).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
									return qrCode(gtx, at.code, black)
								})
							})
						})
					}),

					layout.Rigid(layout.Spacer{Height: gap}.Layout),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						if at.ticket == "" {
							return layout.Dimensions{}
						}
						return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
							layout.Rigid(a.tiny("Point a camera at it, or type this over there:", faint).Layout),
							layout.Rigid(layout.Spacer{Height: tight}.Layout),
							layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								return tinted(gtx, wash, round, func(gtx layout.Context) layout.Dimensions {
									gtx.Constraints.Min.X = gtx.Constraints.Max.X
									return layout.UniformInset(gap).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
										cmd := a.tiny("drop pair "+at.ticket, violet)
										cmd.Font.Typeface = "Go Mono"
										return cmd.Layout(gtx)
									})
								})
							}),
						)
					}),

					layout.Rigid(layout.Spacer{Height: pad}.Layout),
					layout.Rigid(rule),
					layout.Rigid(layout.Spacer{Height: pad}.Layout),

					// The other half: reading a code somebody else is showing.
					layout.Rigid(a.label("Or enter theirs", unit.Sp(15), font.Bold, ink).Layout),
					layout.Rigid(layout.Spacer{Height: tight}.Layout),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
							layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
								return a.field(gtx, &a.joinField, "paste a ticket or drop:// link")
							}),
							layout.Rigid(layout.Spacer{Width: gap}.Layout),
							layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								return a.button(gtx, &a.joinGo, "Pair", true)
							}),
						)
					}),

					layout.Rigid(layout.Spacer{Height: pad}.Layout),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return a.button(gtx, &a.pairStop, "Cancel", false)
					}),
				)
			})
	})
}

// field is the one text input shape.
func (a *App) field(gtx layout.Context, editor *widget.Editor, hint string) layout.Dimensions {
	return tinted(gtx, wash, round, func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		gtx.Constraints.Min.Y = gtx.Dp(touch)

		return layout.Inset{Left: pad, Right: pad}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return layout.W.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				e := material.Editor(a.theme, editor, hint)
				e.Color = ink
				e.HintColor = faint
				e.TextSize = unit.Sp(14)
				return e.Layout(gtx)
			})
		})
	})
}

// joinWhatIsTyped pairs with whoever is showing the ticket in the box.
func (a *App) joinWhatIsTyped() {
	ticket := strings.TrimSpace(a.joinField.Text())
	if ticket == "" {
		return
	}
	a.joinField.SetText("")

	go func() {
		if _, err := a.from.Join(ticket); err != nil {
			a.mu.Lock()
			if a.linking != nil {
				a.linking.err = err.Error()
			}
			a.mu.Unlock()
			a.redraw()
			return
		}

		a.mu.Lock()
		a.linking = nil
		a.mu.Unlock()

		a.loadPeers()
	}()
}
