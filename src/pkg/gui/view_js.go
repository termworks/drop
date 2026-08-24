//go:build js

package gui

import "gioui.org/app"

// A browser has no view to hand anybody, and asks for the camera through the page.
func noteView(app.ViewEvent) {}
