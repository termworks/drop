//go:build js || android

package gui

import (
	"os"

	"gioui.org/app"
	"gioui.org/op"
	"gioui.org/op/paint"
)

// Run opens a window and draws until it is closed.
//
// The same loop everywhere: a browser tab and a phone both hand Gio a surface, and neither the
// screens nor this know which they were given.
func Run(from Source) {
	go func() {
		w := new(app.Window)
		w.Option(app.Title("drop"))

		ui := New(from)
		ui.Start(w.Invalidate)

		var ops op.Ops
		for {
			switch e := w.Event().(type) {
			case app.DestroyEvent:
				if e.Err != nil {
					os.Exit(1)
				}
				os.Exit(0)

			case app.FrameEvent:
				gtx := app.NewContext(&ops, e)

				// The ground is painted first, so a frame never composites onto whatever the host
				// left behind.
				paint.Fill(gtx.Ops, ground)
				ui.Layout(gtx)

				e.Frame(gtx.Ops)
			}
		}
	}()
	app.Main()
}
