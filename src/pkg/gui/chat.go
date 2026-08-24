//go:build js || android

package gui

import (
	"strings"

	"gioui.org/font"
	"gioui.org/layout"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
)

// chat is a conversation and the line for adding to it.
func (a *App) chat(gtx layout.Context, history []Message, trouble string) layout.Dimensions {
	with, ok := a.peer()
	if !ok {
		return layout.Dimensions{}
	}

	for {
		event, found := a.compose.Update(gtx)
		if !found {
			break
		}
		if _, sent := event.(widget.SubmitEvent); sent {
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
			return layout.Inset{Top: gap, Left: pad, Right: pad}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return a.list.Layout(gtx, len(history), func(gtx layout.Context, i int) layout.Dimensions {
					return a.bubble(gtx, history[i])
				})
			})
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			if trouble == "" {
				return layout.Dimensions{}
			}
			return a.note(gtx, trouble, bad)
		}),
		layout.Rigid(rule),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.UniformInset(pad).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
						return a.field(gtx, &a.compose, "Write something")
					}),
					layout.Rigid(layout.Spacer{Width: gap}.Layout),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return a.button(gtx, &a.send, "Send", true)
					}),
				)
			})
		}),
	)
}

func (a *App) sendWhatIsTyped(peer string) {
	body := strings.TrimSpace(a.compose.Text())
	if body == "" {
		return
	}
	a.compose.SetText("")
	go a.say(peer, body)
}

// bubble is one message. Mine carry the brand gradient and theirs a plain card — the difference the
// eye reads before any of the words, which is what lets a conversation be scanned rather than read.
func (a *App) bubble(gtx layout.Context, m Message) layout.Dimensions {
	body := m.Body
	if m.Kind == "file" {
		body = "file · " + m.Body
	}

	inner := func(text, meta interface {
		Layout(layout.Context) layout.Dimensions
	}) layout.Widget {
		return func(gtx layout.Context) layout.Dimensions {
			return layout.UniformInset(gap).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
					layout.Rigid(text.Layout),
					layout.Rigid(layout.Spacer{Height: tight}.Layout),
					layout.Rigid(meta.Layout),
				)
			})
		}
	}

	// A bubble stops well short of the far edge, so which side it is on stays obvious even when the
	// message is long.
	wide := gtx.Constraints.Max.X * 4 / 5

	return layout.Inset{Bottom: gap}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		if m.Mine {
			return layout.E.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				gtx.Constraints.Max.X = wide
				return filled(gtx, round, inner(
					a.label(body, unit.Sp(15), font.Normal, onDark),
					a.tiny(when(m.At), mix(onDark, plum, 0.35)),
				))
			})
		}
		return layout.W.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Max.X = wide
			return card(gtx, round, inner(
				a.label(body, unit.Sp(15), font.Normal, ink),
				a.tiny(when(m.At), faint),
			))
		})
	})
}

// shared is what a phone handed over, sitting above everything until it is told where to go.
func (a *App) shared(gtx layout.Context, item *Shared) layout.Dimensions {
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
	switch a.at {
	case atPaths:
		say = "choose a conversation"
	case atOpen:
		say = "ready to send"
	}

	return tinted(gtx, wash, round, func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints.Min.X = gtx.Constraints.Max.X

		return layout.UniformInset(gap).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							what := a.small(item.What(), ink)
							what.Font.Weight = font.Bold
							what.MaxLines = 2
							return what.Layout(gtx)
						}),
						layout.Rigid(a.tiny("shared with drop · "+say, dim).Layout),
					)
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					if a.at != atOpen {
						return layout.Dimensions{}
					}
					return layout.Inset{Left: gap}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return a.button(gtx, &a.sendShared, "Send", true)
					})
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.Inset{Left: tight}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return a.button(gtx, &a.dropShared, "✕", false)
					})
				}),
			)
		})
	})
}

var _ = material.Editor
