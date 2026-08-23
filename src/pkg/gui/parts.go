//go:build js || android

package gui

import (
	"image/color"

	"gioui.org/layout"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
)

// card is one row of a list: what it is, what kind, and what that kind is for.
//
// Three lines rather than one, because what is worth knowing about a device does not fit on one and
// putting it in a second column instead forces the eye to read across rather than down.
func (a *App) card(gtx layout.Context, click *widget.Clickable, title, tag, about string) layout.Dimensions {
	return layout.Inset{Left: pad, Right: pad, Bottom: gap}.Layout(gtx,
		func(gtx layout.Context) layout.Dimensions {
			return material.Clickable(gtx, click, func(gtx layout.Context) layout.Dimensions {
				return fill(gtx, panel, round, func(gtx layout.Context) layout.Dimensions {
					gtx.Constraints.Min.X = gtx.Constraints.Max.X

					return layout.UniformInset(unit.Dp(14)).Layout(gtx,
						func(gtx layout.Context) layout.Dimensions {
							return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
								layout.Rigid(func(gtx layout.Context) layout.Dimensions {
									return layout.Flex{Alignment: layout.Baseline}.Layout(gtx,
										layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
											name := material.Body1(a.theme, title)
											name.Color = ink
											name.Font.Weight = 650
											return name.Layout(gtx)
										}),
										layout.Rigid(func(gtx layout.Context) layout.Dimensions {
											kind := material.Caption(a.theme, tag)
											kind.Color = violet
											return kind.Layout(gtx)
										}),
									)
								}),
								layout.Rigid(layout.Spacer{Height: unit.Dp(3)}.Layout),
								layout.Rigid(func(gtx layout.Context) layout.Dimensions {
									note := material.Caption(a.theme, about)
									note.Color = dim
									return note.Layout(gtx)
								}),
							)
						})
				})
			})
		})
}

// note is what fills the pane when there is nothing to list.
func (a *App) note(gtx layout.Context, said string, c color.NRGBA) layout.Dimensions {
	return layout.Inset{Top: pad, Left: pad, Right: pad}.Layout(gtx,
		func(gtx layout.Context) layout.Dimensions {
			text := material.Body2(a.theme, said)
			text.Color = c
			return text.Layout(gtx)
		})
}

// chat is a conversation, and the line for adding to it.
func (a *App) chat(gtx layout.Context, history []Message, trouble string) layout.Dimensions {
	with, ok := a.peer()
	if !ok {
		return layout.Dimensions{}
	}

	// Enter sends, which is what an editor with Submit set reports.
	for {
		event, found := a.compose.Update(gtx)
		if !found {
			break
		}
		if _, ended := event.(widget.SubmitEvent); ended {
			a.sendWhatIsTyped(with.Name)
		}
	}
	if a.send.Clicked(gtx) {
		a.sendWhatIsTyped(with.Name)
	}

	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			if len(history) == 0 {
				return a.note(gtx, "Nothing said yet.", faint)
			}
			return a.list.Layout(gtx, len(history), func(gtx layout.Context, i int) layout.Dimensions {
				return a.bubble(gtx, history[i])
			})
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			if trouble != "" {
				return a.note(gtx, trouble, bad)
			}
			return layout.Dimensions{}
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return a.composer(gtx)
		}),
	)
}

func (a *App) sendWhatIsTyped(peer string) {
	body := a.compose.Text()
	if body == "" {
		return
	}
	a.compose.SetText("")
	go a.say(peer, body)
}

// bubble is one message. Mine are filled with the accent and theirs are bordered, which is the
// difference the eye reads before any of the words.
func (a *App) bubble(gtx layout.Context, m Message) layout.Dimensions {
	background, text := panel, ink
	if m.Mine {
		background, text = violet, onDark
	}

	body := m.Body
	if m.Kind == "file" {
		body = "file · " + m.Body
	}

	return layout.Inset{Left: pad, Right: pad, Bottom: gap}.Layout(gtx,
		func(gtx layout.Context) layout.Dimensions {
			return fill(gtx, background, round, func(gtx layout.Context) layout.Dimensions {
				return layout.UniformInset(unit.Dp(11)).Layout(gtx,
					func(gtx layout.Context) layout.Dimensions {
						return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
							layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								said := material.Body2(a.theme, body)
								said.Color = text
								return said.Layout(gtx)
							}),
							layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								at := material.Caption(a.theme, when(m.At))
								at.Color = dim
								if m.Mine {
									at.Color = line
								}
								return at.Layout(gtx)
							}),
						)
					})
			})
		})
}

func (a *App) composer(gtx layout.Context) layout.Dimensions {
	return layout.Inset{Left: pad, Right: pad, Top: gap, Bottom: pad}.Layout(gtx,
		func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					return fill(gtx, panel, round, func(gtx layout.Context) layout.Dimensions {
						gtx.Constraints.Min.X = gtx.Constraints.Max.X
						return layout.UniformInset(unit.Dp(12)).Layout(gtx,
							func(gtx layout.Context) layout.Dimensions {
								field := material.Editor(a.theme, &a.compose, "Write something")
								field.Color = ink
								field.HintColor = faint
								return field.Layout(gtx)
							})
					})
				}),
				layout.Rigid(layout.Spacer{Width: gap}.Layout),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					b := material.Button(a.theme, &a.send, "Send")
					b.Background = violet
					b.Color = onDark
					b.CornerRadius = round
					return b.Layout(gtx)
				}),
			)
		})
}

// banner is what the share sheet handed over, sitting above everything until it is told where to go.
func (a *App) banner(gtx layout.Context, item *Shared) layout.Dimensions {
	if a.dropShared.Clicked(gtx) {
		a.mu.Lock()
		a.pending = nil
		a.mu.Unlock()
	}
	if a.sendShared.Clicked(gtx) {
		if with, ok := a.peer(); ok && a.at == atOpen {
			go a.deliverPending(with.Name)
		}
	}

	// What to say depends on how far in they are, because the button only does something once a
	// conversation is open.
	say := "choose a device"
	if a.at == atPaths {
		say = "choose a conversation"
	}
	if a.at == atOpen {
		say = "send it here"
	}

	return layout.Inset{Left: pad, Right: pad, Bottom: gap}.Layout(gtx,
		func(gtx layout.Context) layout.Dimensions {
			return fill(gtx, soft, round, func(gtx layout.Context) layout.Dimensions {
				gtx.Constraints.Min.X = gtx.Constraints.Max.X

				return layout.UniformInset(unit.Dp(12)).Layout(gtx,
					func(gtx layout.Context) layout.Dimensions {
						return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
							layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								what := material.Body2(a.theme, item.What())
								what.Color = ink
								what.Font.Weight = 650
								what.MaxLines = 2
								return what.Layout(gtx)
							}),
							layout.Rigid(layout.Spacer{Height: unit.Dp(6)}.Layout),
							layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
									layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
										note := material.Caption(a.theme, "shared with drop · "+say)
										note.Color = dim
										return note.Layout(gtx)
									}),
									layout.Rigid(func(gtx layout.Context) layout.Dimensions {
										if a.at != atOpen {
											return layout.Dimensions{}
										}
										b := material.Button(a.theme, &a.sendShared, "Send")
										b.Background = violet
										b.Color = onDark
										b.CornerRadius = round
										b.Inset = layout.UniformInset(unit.Dp(8))
										return b.Layout(gtx)
									}),
									layout.Rigid(layout.Spacer{Width: gap}.Layout),
									layout.Rigid(func(gtx layout.Context) layout.Dimensions {
										b := material.Button(a.theme, &a.dropShared, "Discard")
										b.Background = panel
										b.Color = dim
										b.CornerRadius = round
										b.Inset = layout.UniformInset(unit.Dp(8))
										return b.Layout(gtx)
									}),
								)
							}),
						)
					})
			})
		})
}

// pairingView is the code, the ticket, and the wait — the way out of a device that knows nobody.
func (a *App) pairingView(gtx layout.Context, at *linking) layout.Dimensions {
	if a.pairStop.Clicked(gtx) {
		a.stopPairing()
	}

	return layout.UniformInset(pad).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				title := material.H6(a.theme, "Pair a device")
				title.Color = ink
				return title.Layout(gtx)
			}),
			layout.Rigid(layout.Spacer{Height: gap}.Layout),

			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				if at.err != "" {
					return a.note(gtx, at.err, bad)
				}
				return layout.Dimensions{}
			}),

			// Painted rather than typed, so a module is a square whatever the font happens to do.
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				if at.code == nil {
					return layout.Dimensions{}
				}
				return qrCode(gtx, at.code, ink)
			}),

			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				if at.ticket == "" {
					return layout.Dimensions{}
				}
				return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						note := material.Caption(a.theme, "or run this on the other device:")
						note.Color = dim
						return note.Layout(gtx)
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						cmd := material.Caption(a.theme, "drop pair "+at.ticket)
						cmd.Font.Typeface = "Go Mono"
						cmd.Color = violet
						return cmd.Layout(gtx)
					}),
				)
			}),

			layout.Rigid(layout.Spacer{Height: gap}.Layout),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				waiting := material.Caption(a.theme, "waiting for it to answer…")
				waiting.Color = dim
				return waiting.Layout(gtx)
			}),
			layout.Rigid(layout.Spacer{Height: gap}.Layout),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				b := material.Button(a.theme, &a.pairStop, "Cancel")
				b.Background = panel
				b.Color = dim
				b.CornerRadius = round
				return b.Layout(gtx)
			}),
		)
	})
}

// nothingPaired is the first thing anyone sees, so it offers the way forward rather than naming a
// command they have to go and find a terminal for.
func (a *App) nothingPaired(gtx layout.Context) layout.Dimensions {
	if a.pairNow.Clicked(gtx) {
		go a.startPairing()
	}

	return layout.UniformInset(pad).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				said := material.Body1(a.theme, "No devices yet.")
				said.Color = ink
				return said.Layout(gtx)
			}),
			layout.Rigid(layout.Spacer{Height: unit.Dp(4)}.Layout),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				said := material.Body2(a.theme, "Pair one to send it files, messages, or a terminal.")
				said.Color = dim
				return said.Layout(gtx)
			}),
			layout.Rigid(layout.Spacer{Height: pad}.Layout),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				b := material.Button(a.theme, &a.pairNow, "Pair a device")
				b.Background = violet
				b.Color = onDark
				b.CornerRadius = round
				return b.Layout(gtx)
			}),
		)
	})
}
