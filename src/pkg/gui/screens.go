//go:build js || android

package gui

import (
	"gioui.org/layout"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
)

// Layout draws one frame.
//
// The same three screens everywhere: the devices you know, what one of them shares with you, and
// whatever is at the path. Entering rather than tabbing, because what a path is depends on the
// device it is on.
func (a *App) Layout(gtx layout.Context) layout.Dimensions {
	a.mu.Lock()
	me, peers, paths := a.me, a.peers, a.paths
	history, trouble, busy := a.history, a.trouble, a.busy
	a.mu.Unlock()

	if a.back.Clicked(gtx) {
		a.goBack()
	}
	if a.refresh.Clicked(gtx) {
		go a.loadPeers()
	}

	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return a.header(gtx, me, busy)
		}),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			if trouble != "" && a.at != atOpen {
				return a.note(gtx, trouble, bad)
			}
			switch a.at {
			case atDevices:
				return a.devices(gtx, peers)
			case atPaths:
				return a.pathList(gtx, paths, busy)
			default:
				return a.open(gtx, history, trouble)
			}
		}),
	)
}

// header names where you are and which device this is.
func (a *App) header(gtx layout.Context, me Identity, busy bool) layout.Dimensions {
	return layout.Inset{Top: pad, Bottom: gap, Left: pad, Right: pad}.Layout(gtx,
		func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					if a.at == atDevices {
						return layout.Dimensions{}
					}
					b := material.Button(a.theme, &a.back, "‹")
					b.Background = soft
					b.Color = ink
					b.CornerRadius = round
					b.Inset = layout.UniformInset(unit.Dp(10))
					return b.Layout(gtx)
				}),
				layout.Rigid(layout.Spacer{Width: gap}.Layout),
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					title := material.H6(a.theme, a.where())
					title.Color = ink
					return title.Layout(gtx)
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					said := me.Name
					if busy {
						said = "asking…"
					}
					who := material.Caption(a.theme, said)
					who.Color = faint
					return who.Layout(gtx)
				}),
			)
		})
}

func (a *App) where() string {
	switch a.at {
	case atPaths:
		if with, ok := a.peer(); ok {
			return with.Name
		}
	case atOpen:
		if on, ok := a.path(); ok {
			return on.Path
		}
	}
	return "drop"
}

// devices is the address book, one three-line card each.
func (a *App) devices(gtx layout.Context, peers []Peer) layout.Dimensions {
	if len(peers) == 0 {
		return a.note(gtx, "Nothing paired yet. Run `drop pair` on both devices.", dim)
	}

	a.fitRows(len(peers))

	return a.list.Layout(gtx, len(peers), func(gtx layout.Context, i int) layout.Dimensions {
		p := peers[i]

		if a.rows[i].Clicked(gtx) {
			a.enter(i)
		}

		state := "paired"
		if !p.Paired {
			state = "not paired"
		}
		return a.card(gtx, &a.rows[i], p.Name, state, short(p.ID))
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

	return a.list.Layout(gtx, len(paths), func(gtx layout.Context, i int) layout.Dimensions {
		s := paths[i]

		if a.rows[i].Clicked(gtx) {
			a.enter(i)
		}

		may := "read only"
		if s.Writable {
			may = "you may send"
		}
		if s.Kind == "branch" {
			may = "holds other paths"
		}
		return a.card(gtx, &a.rows[i], s.Path, s.Kind, may+" · "+about(s.Kind))
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
	case "files":
		return a.note(gtx, "A place to send files. Use `drop to` or the share sheet.", dim)
	case "tty", "stream":
		return a.note(gtx, "A live terminal. Not drawn here yet.", dim)
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
		return "serves nothing itself"
	default:
		return ""
	}
}

func short(id string) string {
	if len(id) > 24 {
		return id[:24] + "…"
	}
	return id
}
