//go:build js || android

package gui

import (
	"image"
	"strings"

	"gioui.org/font"
	"gioui.org/layout"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
)

// Layout draws one frame.
//
// Three screens, entered rather than tabbed between: the devices you know, what one of them shares
// with you, and whatever is at the path. What a path is depends on the device it is on, so the two
// are a sequence and not two columns to compare.
func (a *App) Layout(gtx layout.Context) layout.Dimensions {
	a.mu.Lock()
	me, peers, paths := a.me, a.peers, a.paths
	history, trouble, busy := a.history, a.trouble, a.busy
	pending, linking := a.pending, a.linking
	a.mu.Unlock()

	if a.back.Clicked(gtx) {
		a.goBack()
	}
	if a.pairNow.Clicked(gtx) || a.joinNow.Clicked(gtx) {
		go a.startPairing()
	}
	if a.refresh.Clicked(gtx) {
		go a.loadPeers()
	}

	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return a.header(gtx, me, busy)
		}),
		layout.Rigid(rule),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			if pending == nil {
				return layout.Dimensions{}
			}
			return layout.Inset{Top: gap, Left: pad, Right: pad}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return a.shared(gtx, pending)
			})
		}),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			switch {
			case linking != nil:
				return a.pairing(gtx, linking)
			case trouble != "" && a.at != atOpen:
				return a.note(gtx, trouble, bad)
			case a.at == atDevices && len(peers) == 0:
				return a.welcome(gtx)
			case a.at == atDevices:
				return a.devices(gtx, peers)
			case a.at == atPaths:
				return a.pathList(gtx, paths, busy)
			default:
				return a.open(gtx, history, trouble)
			}
		}),
	)
}

// header is where you are and which device this is. The mark is a wordmark rather than the logo:
// Gio draws shapes, not files, and a badly redrawn mark is worse than a well set word.
func (a *App) header(gtx layout.Context, me Identity, busy bool) layout.Dimensions {
	return layout.Inset{Top: pad, Bottom: gap, Left: pad, Right: pad}.Layout(gtx,
		func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					if a.at == atDevices && a.linking == nil {
						return layout.Dimensions{}
					}
					return layout.Inset{Right: gap}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return a.button(gtx, &a.back, "‹", false)
					})
				}),
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
						layout.Rigid(a.title(a.where()).Layout),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							if a.at != atDevices || me.Name == "" || a.linking != nil {
								return layout.Dimensions{}
							}
							return a.tiny("this device is "+me.Name, faint).Layout(gtx)
						}),
					)
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					if busy {
						return a.small("asking…", faint).Layout(gtx)
					}
					if a.at == atDevices && a.linking == nil {
						return a.button(gtx, &a.pairNow, "Pair", true)
					}
					return layout.Dimensions{}
				}),
			)
		})
}

func (a *App) where() string {
	switch {
	case a.linking != nil:
		return "Pair a device"
	case a.at == atPaths:
		if with, ok := a.peer(); ok {
			return with.Name
		}
	case a.at == atOpen:
		if on, ok := a.path(); ok {
			return on.Path
		}
	}
	return "drop"
}

// welcome is the first thing anyone sees, so it offers the way forward rather than naming a command
// they would have to find a terminal for.
func (a *App) welcome(gtx layout.Context) layout.Dimensions {
	// Centred in what is left rather than pinned under the header: on a phone the buttons are
	// then within a thumb of the bottom instead of stranded halfway up an empty screen.
	return layout.Inset{Left: pad, Right: pad}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		// Spacers either side rather than a centring wrapper: centring shrinks the column to its
		// widest child, and the buttons below want the whole width.
		return layout.Flex{Axis: layout.Vertical, Alignment: layout.Middle}.Layout(gtx,
			layout.Flexed(1, layout.Spacer{}.Layout),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Vertical, Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						// The mark sits in a pool of its own colour, which is the one piece of depth
						// on a screen that is otherwise flat panels.
						size := gtx.Dp(unit.Dp(104))
						glow(gtx, image.Rect(size/4, size/4, size*3/4, size*3/4), violet, unit.Dp(64))
						return a.mark(gtx, size)
					}),
					layout.Rigid(layout.Spacer{Height: roomy}.Layout),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return layout.Center.Layout(gtx, a.label("No devices yet", sizeStrong, font.Bold, ink).Layout)
					}),
					layout.Rigid(layout.Spacer{Height: tight}.Layout),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return layout.Center.Layout(gtx, a.small("Pair one to send it files, messages, or a terminal.", dim).Layout)
					}),
					layout.Rigid(layout.Spacer{Height: roomy}.Layout),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return a.button(gtx, &a.pairNow, "Show a code", true)
					}),
					layout.Rigid(layout.Spacer{Height: gap}.Layout),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return a.button(gtx, &a.joinNow, "Enter a ticket", false)
					}),
				)
			}),
			layout.Flexed(1, layout.Spacer{}.Layout),
		)
	})
}

// devices is the address book.
func (a *App) devices(gtx layout.Context, peers []Peer) layout.Dimensions {
	a.fitRows(len(peers))

	return layout.Inset{Top: gap, Left: pad, Right: pad}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return a.list.Layout(gtx, len(peers), func(gtx layout.Context, i int) layout.Dimensions {
			p := peers[i]

			if a.rows[i].Clicked(gtx) {
				a.enter(i)
			}

			state, colour := "paired", good
			if !p.Paired {
				state, colour = "not paired", faint
			}

			return a.row(gtx, &a.rows[i],
				func(gtx layout.Context) layout.Dimensions { return dot(gtx, colour) },
				p.Name, state, short(p.ID), colour)
		})
	})
}

// pathList is what the open device shares with us.
func (a *App) pathList(gtx layout.Context, paths []Space, busy bool) layout.Dimensions {
	if busy && len(paths) == 0 {
		return a.note(gtx, "asking…", dim)
	}
	if len(paths) == 0 {
		return a.note(gtx, "This device shares nothing with you.", dim)
	}

	a.fitRows(len(paths))

	return layout.Inset{Top: gap, Left: pad, Right: pad}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return a.list.Layout(gtx, len(paths), func(gtx layout.Context, i int) layout.Dimensions {
			s := paths[i]

			if a.rows[i].Clicked(gtx) {
				a.enter(i)
			}
			return a.row(gtx, &a.rows[i], nil, s.Path, s.Kind, about(s.Kind), violetOn)
		})
	})
}

// open is whatever is at the path that was entered.
func (a *App) open(gtx layout.Context, history []Message, trouble string) layout.Dimensions {
	on, ok := a.path()
	if !ok {
		return layout.Dimensions{}
	}

	switch on.Kind {
	case "chat":
		return a.chat(gtx, history, trouble)

	case "tty", "stream":
		if a.live == nil {
			return a.note(gtx, "not watching.", faint)
		}
		return layout.Inset{Left: pad, Right: pad, Bottom: pad}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return a.terminal(gtx, a.live)
		})

	case "files":
		with, _ := a.peer()
		return a.note(gtx, "Send files here with  drop to "+with.Name+on.Path+"  <file>", dim)

	default:
		return a.note(gtx, "A "+on.Kind+" path.", dim)
	}
}

func (a *App) fitRows(n int) {
	for len(a.rows) < n {
		a.rows = append(a.rows, widget.Clickable{})
	}
}

func about(kind string) string {
	switch kind {
	case "chat":
		return "messages, kept as a conversation"
	case "files":
		return "send and receive files"
	case "tty":
		return "a terminal, as it is being used"
	case "stream":
		return "output from a command, as it comes"
	case "link", "bookmark":
		return "open a link over there"
	case "branch":
		return "holds other paths"
	default:
		return ""
	}
}

func short(id string) string {
	if len(id) > 28 {
		return id[:28] + "…"
	}
	return id
}

var _ = strings.TrimSpace
var _ = material.H6
