package cmd

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/bresilla/drop/src/pkg/book"
	"github.com/bresilla/drop/src/pkg/dial"
	"github.com/bresilla/drop/src/pkg/node"
	"github.com/bresilla/drop/src/pkg/proto"
	tickets "github.com/bresilla/drop/src/pkg/ticket"
	"github.com/bresilla/drop/src/pkg/user"
)

// Making another machine one of yours. Two ways, because they are two different bargains: vouching
// keeps the key where it is and renews the badge for as long as the machines meet; carrying the key
// over needs nothing ever again and makes a lost phone a lost key.

func newVouchCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "vouch <machine>",
		Short: "Make a machine you paired with one of yours, without handing it your key",
		Long: "Signs a badge saying the machine is yours, and shows it as a code to scan there — on a\n" +
			"phone, Mine → Add a machine. Your key stays here. The badge is signed again\n" +
			"whenever that machine reaches one of yours that can sign without a touch, so it lasts as\n" +
			"long as the two keep meeting; `drop peer forget` it on those machines, and it runs out.",
		Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			entry, err := Entry(args[0])
			if err != nil {
				return err
			}
			badge, sig, err := user.Vouch(entry.ID.String(), entry.Name, time.Now())
			if err != nil {
				return err
			}
			packed, err := user.Pack(badge, sig)
			if err != nil {
				return err
			}
			code := tickets.LinkAs(tickets.KindBadge, base64.RawURLEncoding.EncodeToString(packed))
			showCode(code)
			fmt.Printf("  %s is yours until %s, once it takes this:\n\n  drop me user take %s\n\n",
				entry.Name, badge.Until.UTC().Format("2006-01-02"), code)
			return nil
		},
	}
}

func newExportCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "export",
		Short: "Show your user key as a code, to carry it to another machine",
		Long: "The key itself, as a code to scan. Whoever reads it is you from then on, on every\n" +
			"machine that knows you, so show it only to a machine of yours and only where nobody\n" +
			"else can see the screen. `drop me user vouch` is the way that keeps the key here.",
		Args: cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			seed, err := user.Export()
			if err != nil {
				return err
			}
			code := tickets.LinkAs(tickets.KindKey, base64.RawURLEncoding.EncodeToString(seed))
			showCode(code)
			fmt.Printf("  this is your user key: anybody who reads it is you\n\n  drop me user take %s\n\n", code)
			return nil
		},
	}
}

func newTakeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "take <code>",
		Short: "Become one of somebody's machines, from a code vouch or export showed",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			said, err := TakeCode(args[0])
			if err != nil {
				return err
			}
			fmt.Printf("%s\nrestart drop here for it to wear the new badge\n", said)
			return nil
		},
	}
}

// TakeCode makes this machine one of somebody's, from the code another machine of theirs showed,
// and says what it now is. It is worn from the next start.
func TakeCode(code string) (string, error) {
	kind, body := tickets.Kind(code)
	raw, err := base64.RawURLEncoding.DecodeString(body)
	if err != nil {
		return "", fmt.Errorf("that code is unreadable: %w", err)
	}

	switch kind {
	case tickets.KindBadge:
		badge, sig, err := user.Unpack(raw, time.Now())
		if err != nil {
			return "", err
		}
		if err := user.Wear(badge, sig, time.Now()); err != nil {
			return "", err
		}
		return fmt.Sprintf("this machine is %s's, vouched for until %s",
			user.Fingerprint(badge.User), badge.Until.UTC().Format("2006-01-02")), nil

	case tickets.KindKey:
		if err := user.Import(raw); err != nil {
			return "", err
		}
		pub, err := user.Public()
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("this machine holds %s's key, and signs its own badge", user.Fingerprint(pub)), nil
	}
	return "", errors.New("that is a pairing code: pair with it instead")
}

func showCode(code string) {
	if !term.IsTerminal(int(os.Stdout.Fd())) {
		return
	}
	drawn, err := tickets.CodeOf(code)
	if err != nil {
		fmt.Fprintf(os.Stderr, "drop: could not draw a code: %v\n", err)
		return
	}
	fmt.Printf("\n%s\n", tickets.Painted(drawn))
}

// renewalFor is a fresh badge for a machine of this user's whose own is running low.
//
// Only one written down here, because forgetting a lost phone on the machines that hold the key is
// what lets its badge run out. And only when signing costs nobody a touch: a key in hardware is not
// asked at a moment nobody chose.
func renewalFor(pinned *book.Book, from node.ID, badge proto.Badged) []byte {
	now := time.Now()
	if !badge.Shown() || badge.Key != myKey() || !user.Due(badge.Until, now) {
		return nil
	}
	if entry, ok := pinned.ByID(from); !ok || !entry.Paired() {
		return nil
	}
	by, ok := user.Quiet()
	if !ok {
		return nil
	}
	signed, sig, err := user.Sign(by, from.String(), badge.As, now)
	if err != nil {
		return nil
	}
	return user.Bundle(signed, sig)
}

// learnMine files a machine under this user the first time it shows this user's badge. It was
// paired as whoever it was then; a phone that became one of mine since is mine from then on.
func learnMine(pinned *book.Book, from node.ID) {
	if entry, ok := pinned.ByID(from); !ok || entry.User == myKey() {
		return
	}
	err := pinned.Change(func() (bool, error) {
		entry, ok := pinned.ByID(from)
		if !ok || entry.User == myKey() {
			return false, nil
		}
		pinned.Belongs(entry.Name, myKey())
		return true, nil
	})
	if err != nil {
		trace(fmt.Sprintf("filing %s under me: %v", node.Brief(from), err))
	}
}

// keepBadged asks this user's other machines for a fresh badge while this one's is running low and
// it cannot sign one itself. Asking what one of them serves is enough: whichever can sign answers
// with one.
func keepBadged(ctx context.Context, held *dial.Kept) {
	tick := time.NewTicker(time.Hour)
	defer tick.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Minute):
		}
		if due() {
			if pinned, err := book.Load(); err == nil {
				for _, entry := range pinned.Paired() {
					if entry.User != myKey() || !due() {
						continue
					}
					askedHello(ctx, kept{held: held}, entry)
				}
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

// due reports whether this machine's badge wants signing again by somebody else.
func due() bool {
	if _, quiet := user.Quiet(); quiet {
		return false
	}
	mine.Lock()
	until := mine.until
	mine.Unlock()
	return user.Due(until, time.Now())
}

func askedHello(ctx context.Context, over reaches, entry book.Entry) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	done, s, err := over.To(ctx, entry, node.ALPNHello)
	if err != nil {
		return
	}
	defer func() { _ = done.Close() }()
	defer func() { _ = s.Close() }()
	defer stopStreamOnDone(ctx, s)()

	_, _ = proto.AskHello(s)
}
