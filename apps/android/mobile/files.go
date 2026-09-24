package mobile

import (
	"context"
	"strings"

	"github.com/bresilla/drop/src/pkg/tui"
)

type held struct {
	Name string `json:"name"`
	Size int64  `json:"size"`
	At   int64  `json:"at"`
	Dir  bool   `json:"dir"`
}

// List is one directory inside a files namespace: on a machine, or on this device when the name
// is empty. The empty directory is the namespace's root.
func (n *Node) List(name, path, dir string) (string, error) {
	var (
		all []tui.Held
		err error
	)
	if name == "" {
		all, err = n.back.Holding(path, dir)
	} else {
		on, lookErr := entry(name)
		if lookErr != nil {
			return "", lookErr
		}
		ctx, cancel := context.WithTimeout(n.ctx, reachWithin)
		defer cancel()
		all, err = n.back.Listing(ctx, on, path, dir)
	}
	if err != nil {
		return "", err
	}

	out := make([]held, 0, len(all))
	for _, h := range all {
		out = append(out, held{Name: h.Name, Size: h.Size, At: h.At.UnixMilli(), Dir: h.Dir})
	}
	return encode(out), nil
}

// Fetch copies one file out of a namespace on a machine, and says where on this device it landed.
func (n *Node) Fetch(name, path, dir, file string) (string, error) {
	from, err := entry(name)
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(n.ctx, transferWithin)
	defer cancel()

	at, err := n.back.Fetch(ctx, from, path, dir, file, n.moving)
	changed(n.events)
	return at, err
}

// Put copies one file from this device into a files namespace on a machine.
func (n *Node) Put(name, path, dir, file string) error {
	to, err := entry(name)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(n.ctx, transferWithin)
	defer cancel()

	err = n.back.Put(ctx, to, path, dir, file, n.moving)
	changed(n.events)
	return err
}

// Send hands files to a share on a machine: one path a line.
func (n *Node) Send(name, path, files string) error {
	to, err := entry(name)
	if err != nil {
		return err
	}
	var sending []string
	for _, line := range strings.Split(files, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			sending = append(sending, line)
		}
	}
	ctx, cancel := context.WithTimeout(n.ctx, transferWithin)
	defer cancel()

	err = n.back.Send(ctx, to, path, sending, n.moving)
	changed(n.events)
	return err
}

func (n *Node) moving(name string, done, size int64) {
	if n.events != nil {
		n.events.Moving(name, done, size)
	}
}
