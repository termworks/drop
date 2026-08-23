package cmd

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"

	"github.com/bresilla/drop/src/pkg/book"
	"github.com/bresilla/drop/src/pkg/node"
	"github.com/bresilla/drop/src/pkg/ns"
	"github.com/bresilla/drop/src/pkg/proto"
)

// accepting builds the policy that decides whose sessions to take. Pairing is the gate: a peer
// with no shared secret is refused whatever namespace it asks for.
func accepting(pinned *book.Book, any bool) func(node.ID, proto.Open) (bool, string) {
	return func(from node.ID, open proto.Open) (bool, string) {
		if any {
			return true, ""
		}
		entry, ok := pinned.ByID(from)
		if !ok || !entry.Paired() {
			return false, "not paired with you"
		}
		return true, ""
	}
}

// gather turns command line arguments into sources, with - meaning standard input.
func gather(args []string, stdinName string) ([]proto.Source, error) {
	var sources []proto.Source

	for _, path := range args {
		if path == "-" {
			sources = append(sources, proto.FileFromReader(stdinName, os.Stdin))
			continue
		}
		src, err := proto.FileFromPath(path)
		if err != nil {
			return nil, err
		}
		sources = append(sources, src)
	}
	return sources, nil
}

// streamOver reports whether an error is just the far end going away, which is how every stream
// ends. Reporting that as a failure trains people to ignore the last line.
func streamOver(err error) bool {
	if err == nil || errors.Is(err, io.EOF) || false {
		return true
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, os.ErrClosed) {
		return true
	}
	text := err.Error()
	return strings.Contains(text, "connection closed") || strings.Contains(text, "stream reset")
}

// greeting is what this node answers a hello with.
//
// The namespace list goes only to a peer that is paired: what a device serves says a great deal
// about it, and hello is answered by anyone who dials.
func greeting(pinned *book.Book, mounts *ns.Table, from node.ID) proto.Hello {
	hello := proto.Hello{Name: node.DisplayName(), Version: version}

	if entry, ok := pinned.ByID(from); ok && entry.Paired() {
		hello.Serves = proto.Describe(mounts)
	}
	return hello
}
