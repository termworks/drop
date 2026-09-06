package cmd

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bresilla/drop/src/pkg/arch/files"
	"github.com/bresilla/drop/src/pkg/book"
	"github.com/bresilla/drop/src/pkg/node"
	"github.com/bresilla/drop/src/pkg/ns"
	"github.com/bresilla/drop/src/pkg/proto"
)

type mustNotReach struct{}

func (mustNotReach) To(context.Context, book.Entry, string) (io.Closer, proto.Stream, error) {
	panic("reached a peer with a stale address book")
}

func corruptAddressBook(t *testing.T, pinned *book.Book) {
	t.Helper()

	if err := pinned.Save(); err != nil {
		t.Fatalf("Save(): %v", err)
	}

	config, err := node.ConfigDir()
	if err != nil {
		t.Fatalf("ConfigDir(): %v", err)
	}
	if err := os.WriteFile(filepath.Join(config, "peers.json"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestFetchingFailsClosedOnAnUnreadableBook(t *testing.T) {
	key := asSomebody(t)
	pinned, _ := bookWith(t, key)
	corruptAddressBook(t, pinned)

	shared := ns.Shared{Creator: key, At: "/notes", Nonce: "cafe"}
	fetch := fetching(context.Background(), mustNotReach{}, mounted(t, "/notes", shared, []string{"bob"}), pinned)
	err := fetch(files.Wanted{Path: "/notes", Name: "entry"})
	if err == nil || !strings.Contains(err.Error(), "refreshing the address book") {
		t.Fatalf("fetch returned %v, want an address book refresh error", err)
	}
}

func TestReachingFailsClosedOnAnUnreadableBook(t *testing.T) {
	key := asSomebody(t)
	pinned, _ := bookWith(t, key)
	corruptAddressBook(t, pinned)

	shared := ns.Shared{Creator: key, At: "/notes", Nonce: "cafe"}
	reaching(context.Background(), mustNotReach{}, "/notes", mounted(t, "/notes", shared, []string{"bob"}), pinned)
}

func TestMeetingFailsClosedOnAnUnreadableBook(t *testing.T) {
	key := asSomebody(t)
	pinned, _ := bookWith(t, key)
	corruptAddressBook(t, pinned)

	shared := ns.Shared{Creator: key, At: "/notes", Nonce: "cafe"}
	table := mounted(t, "/notes", shared, []string{"bob"})
	err := meeting(table, pinned, nil)(proto.Meeting{Mount: ns.Mount{Path: "/notes", Shared: shared}})
	if err == nil || !strings.Contains(err.Error(), "refreshing the address book") {
		t.Fatalf("meeting returned %v, want an address book refresh error", err)
	}
}
