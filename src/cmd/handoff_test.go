package cmd

import (
	"testing"

	"github.com/bresilla/drop/src/pkg/ns"
)

// A handoff stands for one transfer. A session that landed nothing is not it: a peer that opened
// the path and hung up, or one whose file failed its digest, must leave the path up so the sender
// can come back to the part file it left behind.
func TestAHandoffSurvivesASessionThatTookNothing(t *testing.T) {
	host := newShareHost(ns.NewTable(), (&doings{}).serving())

	box, err := host.begin(t.TempDir(), nil)
	if err != nil {
		t.Fatalf("begin(): %v", err)
	}
	defer host.end(box)

	host.finished(SharePath)
	select {
	case <-box.done:
		t.Fatal("a session that took nothing in closed the handoff")
	default:
	}
}

// A mount answers for everything under it, so a peer pushing to /share/anything is pushing into
// the handoff and ends it like anybody else.
func TestAHandoffEndsOnASubpathToo(t *testing.T) {
	host := newShareHost(ns.NewTable(), (&doings{}).serving())

	box, err := host.begin(t.TempDir(), nil)
	if err != nil {
		t.Fatalf("begin(): %v", err)
	}
	defer host.end(box)

	host.took()
	host.finished(SharePath + "/a")

	select {
	case <-box.done:
	default:
		t.Fatal("a transfer through a path under the handoff left it open")
	}
}
