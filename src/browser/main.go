//go:build js

// Command browser is drop as a page.
//
// A browser cannot open a UDP socket, so this is not a node: it talks to the machine that served it,
// which is one. Everything a person sees is the same Gio code the phone runs.
package main

import "github.com/bresilla/drop/src/pkg/gui"

func main() {
	// An empty address means the origin this was served from, which is the bridge by definition.
	gui.Run(gui.NewRemote(""))
}
