package cmd

import (
	"context"
	"fmt"

	"github.com/bresilla/drop/src/pkg/among"
	"github.com/bresilla/drop/src/pkg/arch/files"
	"github.com/bresilla/drop/src/pkg/book"
	"github.com/bresilla/drop/src/pkg/node"
	"github.com/bresilla/drop/src/pkg/ns"
	"github.com/bresilla/drop/src/pkg/proto"
)

// Getting the bytes of a file a shared folder does not have yet.
//
// A change says which path moved and what it now is; the bytes of a large file are not in it and
// were never going to be. They come over the path bytes already come over here — a files session on
// the machine that has them, the same one anybody walking that namespace opens — and because the
// digest is known before the asking, a fetch that is cut off carries on where it stopped.

// fetching is how a folder gets what it is missing: from whoever else the access rule says holds
// the namespace, whichever of them answers first.
func fetching(ctx context.Context, over reaches, mounts *ns.Table, pinned *book.Book) func(files.Wanted) error {
	return func(w files.Wanted) error {
		mount, _, ok := mounts.Lookup(w.Path)
		if !ok || !mount.Shared.Declared() {
			return fmt.Errorf("%s is not a namespace anybody else holds", w.Path)
		}

		_ = pinned.Refresh()
		rule, _ := mounts.AccessFor(mount.Path)

		holders := among.Holders(rule, pinned)
		if len(holders) == 0 {
			return fmt.Errorf("nobody this machine knows of holds %s", w.Path)
		}

		var last error
		for _, entry := range holders {
			last = fetchFrom(ctx, over, entry, mount.Path, w)
			if last == nil {
				return nil
			}
		}
		return last
	}
}

// fetchFrom asks one machine for one file.
func fetchFrom(ctx context.Context, over reaches, entry book.Entry, at string, w files.Wanted) error {
	done, s, err := over.To(ctx, entry, node.ALPNSession)
	if err != nil {
		return fmt.Errorf("reaching %s: %w", entry.Name, err)
	}
	defer done.Close()
	defer s.Close()

	conn, err := proto.Open(s, at, "files", 0, "", node.DisplayName())
	if err != nil {
		return fmt.Errorf("opening %s on %s: %w", at, entry.Name, err)
	}
	b, err := files.Browse(conn)
	if err != nil {
		return err
	}
	return b.Get(w.Name, w.Into, files.Want{Sum: w.Sum})
}
