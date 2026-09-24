package cmd

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/spf13/cobra"

	"github.com/bresilla/drop/src/pkg/node"
	"github.com/bresilla/drop/src/pkg/proto"
	"github.com/bresilla/drop/src/pkg/user"
)

// Badges signed again with a key that is not always able to sign.
//
// A key in a file signs a machine's fresh badge the moment that machine says hello with one running
// low. A key in a YubiKey cannot: it may not be plugged in, it may want a touch, and a hello is no
// moment to wait for either. So the machines whose badges are running low are written down as they
// say hello, and signed for when the key can — straight away for a key that needs no touch, when
// somebody touches it for one that does, or when asked with `drop machine renew` — and each fresh
// badge goes over the next time its machine says hello.

// renewals is every machine of this user's whose badge is running low, and what has been signed for
// them and not yet handed over.
type renewals struct {
	mu     sync.Mutex
	due    map[string]string
	signed map[string][]byte
	tried  map[string]time.Time
	nudge  chan struct{}
}

var renewing = &renewals{
	due:    map[string]string{},
	signed: map[string][]byte{},
	tried:  map[string]time.Time{},
	nudge:  make(chan struct{}, 1),
}

// retryEvery is how long a badge that could not be signed waits before it is tried again: a key
// that wants a touch blinks each time, and twice a day is often enough inside sixty days.
const retryEvery = 12 * time.Hour

// ready is the badge signed again for a machine, when one is waiting, and otherwise writes down that
// its badge is running low.
func (r *renewals) ready(from node.ID, badge proto.Badged) []byte {
	r.mu.Lock()
	defer r.mu.Unlock()

	id := from.String()
	if bundle, ok := r.signed[id]; ok {
		return bundle
	}
	if _, known := r.due[id]; !known {
		r.due[id] = badge.As
		select {
		case r.nudge <- struct{}{}:
		default:
		}
	}
	return nil
}

// took forgets a machine whose badge is no longer running low: it has the fresh one.
func (r *renewals) took(from node.ID) {
	r.mu.Lock()
	defer r.mu.Unlock()

	id := from.String()
	delete(r.due, id)
	delete(r.signed, id)
	delete(r.tried, id)
}

// waiting is every machine whose badge is running low and not yet signed for, by id, with its name.
func (r *renewals) waiting() map[string]string {
	r.mu.Lock()
	defer r.mu.Unlock()

	out := map[string]string{}
	for id, name := range r.due {
		if _, done := r.signed[id]; !done {
			out[id] = name
		}
	}
	return out
}

// sign signs for every machine waiting, tried or not, and says how many it signed. Each one is a
// signature, so a key that wants a touch wants one each.
func (r *renewals) sign(now time.Time, all bool) (int, error) {
	waiting := r.waiting()
	ids := make([]string, 0, len(waiting))
	for id := range waiting {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	done := 0
	var failed []error
	for _, id := range ids {
		r.mu.Lock()
		last, tried := r.tried[id]
		r.mu.Unlock()
		if !all && tried && now.Sub(last) < retryEvery {
			continue
		}

		signed, sig, err := user.Vouch(id, waiting[id], now)
		r.mu.Lock()
		r.tried[id] = now
		if err == nil {
			r.signed[id] = user.Bundle(signed, sig)
			done++
		}
		r.mu.Unlock()
		if err != nil {
			failed = append(failed, fmt.Errorf("%s: %w", waiting[id], err))
		}
	}
	return done, errors.Join(failed...)
}

// keepRenewing signs for machines as they turn up running low, trying each again twice a day.
func keepRenewing(ctx context.Context) {
	tick := time.NewTicker(time.Hour)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		case <-renewing.nudge:
		}
		if _, quiet := user.Quiet(); quiet {
			continue
		}
		_, _ = renewing.sign(time.Now(), false)
	}
}

// Renew signs every badge running low now, a touch each for a key that wants one, and says how many.
func (l *running) Renew(ctx context.Context) (int, error) {
	if l.daemon {
		said, err := atDaemon(ctx, "renew")
		if err != nil {
			return 0, err
		}
		var n int
		_, err = fmt.Sscanf(said, "renewed %d", &n)
		return n, err
	}
	return renewing.sign(time.Now(), true)
}

// Renewing is how many badges are running low and waiting for the key.
func (l *running) Renewing() int {
	if l.daemon {
		said, err := atDaemon(context.Background(), "renewing")
		if err != nil {
			return 0
		}
		var n int
		_, _ = fmt.Sscanf(said, "renewing %d", &n)
		return n
	}
	return len(renewing.waiting())
}

func newRenewCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "renew",
		Short: "Sign fresh badges for your machines whose badges are running low",
		Long: "With your key in a file this happens by itself. With a key in a YubiKey it happens when the\n" +
			"key is there to sign: this signs every badge running low now, a touch each when the key\n" +
			"wants one, and each machine takes its fresh badge the next time it says hello.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			said, err := atDaemon(cmd.Context(), "renewing")
			if errors.Is(err, errNoDaemon) {
				return errNeedsDaemon
			}
			if err != nil {
				return err
			}
			if said == "renewing 0" {
				fmt.Println("no badge of yours is running low")
				return nil
			}
			fmt.Println("touch your key when it blinks, once for each machine")
			said, err = atDaemon(cmd.Context(), "renew")
			if err != nil {
				return err
			}
			fmt.Println(said)
			return nil
		},
	}
}
