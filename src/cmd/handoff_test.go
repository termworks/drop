package cmd

import (
	"testing"

	"github.com/bresilla/drop/src/pkg/arch/share"
	"github.com/bresilla/drop/src/pkg/made"
	"github.com/bresilla/drop/src/pkg/node"
	"github.com/bresilla/drop/src/pkg/ns"
)

// A handoff stands for one transfer. A session that landed nothing is not it: a peer that opened
// the path and hung up, or one whose file failed its digest, must leave the path up so the sender
// can come back to the part file it left behind.
func TestAHandoffStaysOpenWithoutCompletion(t *testing.T) {
	host := newShareHost(ns.NewTable(), (&doings{}).serving())

	box, err := host.begin(t.TempDir(), nil)
	if err != nil {
		t.Fatalf("begin(): %v", err)
	}
	defer host.end(box)

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

	host.finished(node.ID{}, SharePath+"/a", box.config)

	select {
	case <-box.done:
	default:
		t.Fatal("a transfer through a path under the handoff left it open")
	}
}

func TestAConfiguredShareCannotCompleteAHandoff(t *testing.T) {
	mounts := ns.NewTable()
	known := (&doings{}).serving()
	answers, _ := known.Lookup("share", 0)
	value, err := answers.Read(saying{"dir": t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	configured := value.(share.Config)
	if err := mounts.Add(ns.Mount{Path: "/inbox", Archetype: "share", Config: configured}); err != nil {
		t.Fatal(err)
	}
	host := newShareHost(mounts, known)
	box, err := host.begin(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer host.end(box)

	host.finished(node.ID{}, "/inbox", configured)
	select {
	case <-box.done:
		t.Fatal("a configured share completed the handoff")
	default:
	}
	host.finished(node.ID{}, SharePath, box.config)
	select {
	case <-box.done:
	default:
		t.Fatal("the handoff's own completion left it open")
	}
}

func TestALateCompletionCannotCloseAReplacementHandoff(t *testing.T) {
	host := newShareHost(ns.NewTable(), (&doings{}).serving())
	dir := t.TempDir()
	first, err := host.begin(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	host.end(first)
	second, err := host.begin(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer host.end(second)

	host.finished(node.ID{}, SharePath, first.config)
	select {
	case <-second.done:
		t.Fatal("the old handoff's completion closed its replacement")
	default:
	}
	host.finished(node.ID{}, SharePath, second.config)
	host.finished(node.ID{}, SharePath, second.config)
	select {
	case <-second.done:
	default:
		t.Fatal("the replacement handoff did not complete")
	}
}

func TestAHandoffAndACreatedNamespaceCannotReplaceEachOther(t *testing.T) {
	table := ns.NewTable()
	known := (&doings{}).serving()
	handoffs := newShareHost(table, known)
	created := newMountHost(table, known)
	line := made.Line{Path: SharePath, Entry: made.Entry{
		Archetype: "chat",
		Access:    made.Access{Paired: true},
	}}

	if err := created.begin(line); err != nil {
		t.Fatal(err)
	}
	if _, err := handoffs.begin(t.TempDir(), nil); err == nil {
		t.Fatal("a handoff replaced the held namespace")
	}
	created.end(SharePath)

	box, err := handoffs.begin(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	line.Keep = true
	if err := created.begin(line); err == nil {
		t.Fatal("a written namespace replaced the live handoff")
	}
	if mount, _, ok := table.Lookup(SharePath); !ok || mount.Archetype != "share" {
		t.Fatal("the refused namespace changed the handoff mount")
	}
	handoffs.end(box)
}
