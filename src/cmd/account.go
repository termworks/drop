package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/bresilla/drop/src/pkg/book"
	"github.com/bresilla/drop/src/pkg/convo"
	"github.com/bresilla/drop/src/pkg/node"
	"github.com/bresilla/drop/src/pkg/user"
)

// Leaving your machines, and starting over.
//
// Both are things one machine does to itself, and both are told to the rest of your machines
// first: a machine that simply stopped being yours would go on being listed as yours everywhere
// else, which is the same thing as not having left.

// farewell tells every machine of this user's this one can reach that it is going, so each of them
// forgets it rather than holding on to a machine that is no longer theirs.
func farewell(ctx context.Context, over reaches) {
	self, err := node.LocalID()
	if err != nil {
		return
	}
	if err := user.Remove(self.String(), time.Now()); err != nil {
		return
	}
	pinned, err := book.Load()
	if err != nil {
		return
	}
	for _, entry := range pinned.All() {
		if entry.User == "" || entry.User != myKey() {
			continue
		}
		reach, cancel := context.WithTimeout(ctx, 10*time.Second)
		_ = syncWith(reach, over, pinned, entry)
		cancel()
	}
}

// leaveMine takes this machine back out of its user's: the rest of them are told, it forgets them,
// and it is its own again with the key it had before.
func leaveMine(ctx context.Context, over reaches) error {
	was := myKey()
	farewell(ctx, over)
	if err := user.Leave(); err != nil {
		return err
	}
	pinned, err := book.Load()
	if err != nil {
		return err
	}
	if err := pinned.Change(func() (bool, error) {
		wrote := false
		for _, entry := range pinned.All() {
			if was != "" && entry.User == was {
				pinned.Remove(entry.Name)
				wrote = true
			}
		}
		return wrote, nil
	}); err != nil {
		return err
	}
	return forgetFiles(configFiles("circle", "gone.json"))
}

// startOver deletes everything drop knows on this machine but which machine it is and its config:
// everybody it paired with, the machines it was one of, who may open what, the user key, and every
// conversation. The rest of this user's machines are told first.
func startOver(ctx context.Context, over reaches) error {
	farewell(ctx, over)

	all := configFiles("peers.json", "gone.json", "circle", "badge", "badge.sig", "grants.json", "paths.json", "handle.json")
	if where, err := user.Where(); err == nil && !user.Named() {
		all = append(all, where, where+".pub", where+".before", where+".before.pub")
	}
	if dir, err := convo.DataDir(); err == nil {
		all = append(all, dir)
	}
	return forgetFiles(all)
}

// configFiles is each name, where this machine keeps its things.
func configFiles(names ...string) []string {
	dir, err := node.ConfigDir()
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(names)*2)
	for _, name := range names {
		out = append(out, filepath.Join(dir, name), filepath.Join(dir, name+".lock"))
	}
	return out
}

// forgetFiles removes each of them, whatever it is, and says which could not go.
func forgetFiles(all []string) error {
	var failed []error
	for _, at := range all {
		if err := os.RemoveAll(at); err != nil && !errors.Is(err, os.ErrNotExist) {
			failed = append(failed, err)
		}
	}
	return errors.Join(failed...)
}

// overHere is how this command reaches the rest of this user's machines: through the daemon when one
// runs, which is the only thing that can, and not at all otherwise.
func overHere() reaches {
	if daemonUp(context.Background()) {
		return borrowed{fallback: nobody{}}
	}
	return nobody{}
}

// restartDaemon starts the daemon again when a service runs it, so it forgets what it held in
// memory; and says how to when it cannot.
func restartDaemon() {
	if said := againDaemon(); said != "" {
		fmt.Println("  " + said)
	}
}

// againDaemon restarts the daemon when a service runs it, and says what happened.
func againDaemon() string {
	if !daemonUp(context.Background()) {
		return ""
	}
	if err := exec.Command("systemctl", "--user", "restart", "drop").Run(); err == nil {
		return "drop serve was started again"
	}
	return "restart drop serve, so it forgets what it held"
}

func newLeaveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "leave",
		Short: "Take this machine back out of your machines",
		Long: "The rest of your machines are told first and forget it; this machine forgets them, and is\n" +
			"its own again with the key it had before it joined.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := leaveMine(cmd.Context(), overHere()); err != nil {
				return err
			}
			fmt.Println("this machine is its own again")
			restartDaemon()
			return nil
		},
	}
}

func newStartOverCmd() *cobra.Command {
	var sure bool
	cmd := &cobra.Command{
		Use:   "reset",
		Short: "Delete everything drop knows on this machine, and start over",
		Long: "Everybody this machine paired with, the machines it was one of, who may open what, the\n" +
			"user key and every conversation are deleted; your other machines are told first and forget\n" +
			"it. Which machine this is and its config stay. It asks nothing: pass --yes.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !sure {
				return errors.New("this deletes everything drop knows here: run it again with --yes")
			}
			if err := startOver(cmd.Context(), overHere()); err != nil {
				return err
			}
			fmt.Println("everything is gone; this machine starts over as nobody's")
			restartDaemon()
			return nil
		},
	}
	cmd.Flags().BoolVar(&sure, "yes", false, "really delete everything")
	return cmd
}

// Leave takes this machine back out of its user's machines, telling the rest of them first.
func (l *running) Leave(ctx context.Context) error {
	if l.daemon {
		err := leaveMine(ctx, borrowed{fallback: nobody{}})
		restartDaemon()
		return err
	}
	return leaveMine(ctx, kept{held: l.held})
}

// StartOver deletes everything drop knows on this machine, telling the rest of its user's first.
func (l *running) StartOver(ctx context.Context) error {
	if l.daemon {
		err := startOver(ctx, borrowed{fallback: nobody{}})
		restartDaemon()
		return err
	}
	return startOver(ctx, kept{held: l.held})
}
