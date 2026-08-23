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
