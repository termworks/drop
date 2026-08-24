//go:build js || android

package gui

import (
	"gioui.org/font"
	"gioui.org/layout"
	"gioui.org/op"
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
	if a.scanGo.Clicked(gtx) {
		a.scanForATicket()
	}
	// A viewfinder is only live if something asks for the next frame.
	if scanFrame() != nil {
		gtx.Execute(op.InvalidateCmd{})
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

					// What the camera is looking at, while it is looking. It takes the place of the
					// code rather than sitting beside it: on a phone there is room for one large
					// thing, and while scanning that thing is the viewfinder.
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						frame := scanFrame()
						if frame == nil {
							return layout.Dimensions{}
						}
						return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
							layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								return a.viewfinder(gtx, frame)
							}),
							layout.Rigid(layout.Spacer{Height: gap}.Layout),
							layout.Rigid(a.small("Looking for a code…", dim).Layout),
							layout.Rigid(layout.Spacer{Height: pad}.Layout),
						)
					}),

					// The code, on a white card so a camera has the contrast it wants whatever the
					// surrounding page is doing.
					// Given room rather than whatever is left over. A code shrunk to fit is one a camera
					// cannot resolve, and the rest of this screen can scroll instead.
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						if at.code == nil || scanFrame() != nil {
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
										cmd := a.tiny("drop pair "+at.ticket, violetOn)
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

					// The other half: reading a code somebody else is showing. With a camera that is
					// pointing at it rather than typing a hundred characters off another screen.
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						if !canScan() {
							return layout.Dimensions{}
						}
						return a.button(gtx, &a.scanGo, "Scan their code", true)
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						if !canScan() {
							return layout.Dimensions{}
						}
						return layout.Spacer{Height: pad}.Layout(gtx)
					}),

					layout.Rigid(a.label("Or enter theirs", sizeBody, font.Bold, ink).Layout),
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
				e.TextSize = sizeBody
				return e.Layout(gtx)
			})
		})
	})
}

// scanForATicket opens the camera and pairs with whatever it reads.
//
// A code on another screen is a ticket, so what comes back goes down the same path as a pasted one:
// there is nothing about a scanned ticket that makes it more trustworthy than a typed one.
func (a *App) scanForATicket() {
	err := startScan(scanning{
		found:  a.joinTicket,
		failed: a.pairingWentWrong,
	})
	if err != nil {
		a.pairingWentWrong(err)
	}
}

func (a *App) pairingWentWrong(err error) {
	a.mu.Lock()
	if a.linking != nil {
		a.linking.err = err.Error()
	}
	a.mu.Unlock()
	a.redraw()
}

// joinWhatIsTyped pairs with whoever is showing the ticket in the box.
func (a *App) joinWhatIsTyped() {
	ticket := strings.TrimSpace(a.joinField.Text())
	if ticket == "" {
		return
	}
	a.joinField.SetText("")

	a.joinTicket(ticket)
}

// joinTicket pairs with whoever is showing this ticket, however it arrived.
func (a *App) joinTicket(ticket string) {
	go func() {
		if _, err := a.from.Join(ticket); err != nil {
			a.pairingWentWrong(err)
			return
		}

		a.mu.Lock()
		a.linking = nil
		a.mu.Unlock()

		a.loadPeers()
	}()
}
