package cmd

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/bresilla/drop/src/pkg/arch"
	"github.com/bresilla/drop/src/pkg/conf"
	"github.com/bresilla/drop/src/pkg/made"
	"github.com/bresilla/drop/src/pkg/node"
	"github.com/bresilla/drop/src/pkg/ns"
)

// A namespace made from the command line: up for as long as the command runs, or written down
// beside the config so that it is here after a restart. Two states and no third.

func newCreateCmd() *cobra.Command {
	var (
		text, on, lists []string
		access, visible string
		version         int
		kept, sharing   bool
	)

	cmd := &cobra.Command{
		Use:   "create [path] [type]",
		Short: "Put a namespace up on this machine",
		Long: "create declares a namespace without editing the config. What the type means is the\n" +
			"archetype's own business: the settings are handed to it by name, and this command\n" +
			"knows none of them.\n\n" +
			"  drop path create /notes files --set dir=~/notes --flag writable --access paired\n" +
			"  drop path create /log stream --set command=\"journalctl -f\" --access bob --keep\n\n" +
			"Without --keep the path is up for as long as this command runs and goes when you\n" +
			"stop it. With --keep it is written down as well, and is here after a restart.\n\n" +
			"A setting is text, on or off, or a list of names, and each has a flag of its own,\n" +
			"because a single one that guessed could not say that a piece of text is the word\n" +
			"\"true\".\n\n" +
			"With no arguments it lists the types this build answers to.",
		Args: cobra.MaximumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			known := reading()
			if len(args) == 0 {
				return showArchetypes(known)
			}
			if len(args) == 1 {
				return fmt.Errorf("%s needs a type: run `drop path create` to see what this build answers to", args[0])
			}

			declared, err := settings(text, on, lists)
			if err != nil {
				return err
			}
			rule, err := admitting(access, visible)
			if err != nil {
				return err
			}
			entry := made.Entry{Archetype: args[1], Version: version, Settings: declared, Access: rule}
			if sharing {
				if entry.Shared, err = minted(args[0]); err != nil {
					return err
				}
			}

			return runCreate(cmd.Context(), known, args[0], entry, kept)
		},
	}

	cmd.Flags().StringArrayVar(&text, "set", nil, "key=value, a setting read as text")
	cmd.Flags().StringArrayVar(&on, "flag", nil, "key[=false], a setting read as on or off")
	cmd.Flags().StringArrayVar(&lists, "list", nil, "key=a,b, a setting read as a list of names")
	cmd.Flags().StringVar(&access, "access", "", "paired, trusted, anyone, or a comma-separated list of names")
	cmd.Flags().StringVar(&visible, "visible", "", "who may see it without being able to open it")
	cmd.Flags().IntVar(&version, "version", 0, "which revision of the type answers; the newest by default")
	cmd.Flags().BoolVar(&sharing, "share", false, "several machines hold it, and whoever the rule names may join it")
	cmd.Flags().BoolVar(&kept, "keep", false, "write it down, so it is here after a restart")

	return cmd
}

// minted names a namespace several machines are going to hold.
//
// The name is worked out from who made it, where, and a word telling one thing at that path from
// another made there later — and a command mints that word, so a path taken down and put up again
// is a new thing rather than the old one wearing its history.
func minted(at string) (ns.Shared, error) {
	path, err := ns.Clean(at)
	if err != nil {
		return ns.Shared{}, err
	}
	if myKey() == "" {
		return ns.Shared{}, errors.New("this machine has no user key, so it cannot name what it shares")
	}

	var word [8]byte
	if _, err := rand.Read(word[:]); err != nil {
		return ns.Shared{}, fmt.Errorf("naming %s: %w", path, err)
	}
	return ns.Shared{Creator: myKey(), At: path, Nonce: hex.EncodeToString(word[:])}, nil
}

func newPathRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "rm <path>",
		Short: "Take a created namespace off the list",
		Long: "rm forgets a namespace `drop path create --keep` wrote down. A path the config\n" +
			"declares is not this command's to remove, and it says which file to edit instead.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRemove(args[0])
		},
	}
}

// showArchetypes lists what this build answers to, in the archetypes' own words.
func showArchetypes(known *arch.Registry) error {
	fmt.Println()
	for _, name := range known.Names() {
		answers, ok := known.Lookup(name, 0)
		if !ok {
			continue
		}
		fmt.Printf("  %-10s %s\n", name, answers.Note(nil).About)
	}
	fmt.Println("\n  drop path create <path> <type> --access paired")
	return nil
}

func runCreate(parent context.Context, known *arch.Registry, at string, entry made.Entry, kept bool) error {
	at, err := ns.Clean(at)
	if err != nil {
		return err
	}

	// Read here as well as on the node, so a declaration the archetype refuses is refused before
	// anything is written down and before a socket is dialled.
	answers, ok := known.Lookup(entry.Archetype, entry.Version)
	if !ok {
		return known.Missing(entry.Archetype, entry.Version)
	}
	settings, err := answers.Read(made.Declared(entry.Settings))
	if err != nil {
		return fmt.Errorf("%s: %w", at, err)
	}
	if entry.Shared.Declared() && !answers.Note(settings).Shareable {
		return fmt.Errorf("a %s is one machine's own, so it cannot be shared", entry.Archetype)
	}

	cfg, err := conf.Load(known)
	if err != nil {
		return err
	}
	defer cfg.Close()

	if declares(cfg, at) {
		return fmt.Errorf("%s declares %s already, so it is not this command's to put up", where(cfg), at)
	}

	store, err := made.Load()
	if err != nil {
		return err
	}
	file, err := made.Path()
	if err != nil {
		return err
	}

	if !kept {
		// A path that was written down is meant to outlast a command, and one put up over it for a
		// moment would take it away again on the way out.
		if _, ok := store.Get(at); ok {
			return fmt.Errorf("%s is written down in %s; `drop path rm %s` first, or --keep to change it", at, file, at)
		}
		return holdCreated(parent, at, entry)
	}
	return keepCreated(store, file, at, entry)
}

// keepCreated writes a namespace down and then puts it up, in that order: a node that is not
// running is the ordinary case, and losing the file because there was nothing to tell would be the
// one outcome nobody asked for.
func keepCreated(store *made.Store, file, at string, entry made.Entry) error {
	if err := store.Add(at, entry); err != nil {
		return err
	}

	conn, err := asking()
	if errors.Is(err, errNoNode) {
		fmt.Printf("%s is written down in %s; nothing is serving here, so it starts with `drop serve`\n", at, file)
		return nil
	}
	if err != nil {
		return err
	}
	defer conn.Close()

	if _, err := tell(conn, made.Line{Path: at, Keep: true, Entry: entry}); err != nil {
		return err
	}
	fmt.Printf("%s is up, and written down in %s\n", at, file)
	return nil
}

// holdCreated puts a namespace up for as long as this command runs.
//
// Through the node that is already running, and only that one: a second endpoint on this identity
// is not reachable at the address everybody has written down, so a namespace it served would be one
// nobody could find. It is the same reason a handoff goes this way.
func holdCreated(parent context.Context, at string, entry made.Entry) error {
	conn, err := asking()
	if err != nil {
		return err
	}
	defer conn.Close()

	ctx, stop := signal.NotifyContext(parent, os.Interrupt, syscall.SIGTERM)
	defer stop()

	if _, err := tell(conn, made.Line{Path: at, Entry: entry}); err != nil {
		return err
	}

	id, err := node.LocalID()
	if err != nil {
		return err
	}
	fmt.Printf("\n%s%s  →  %s\n\n", node.DisplayName(), at, entry.Archetype)
	fmt.Printf("  drop connect %s:%s\n\n", node.Brief(id), at)
	fmt.Printf("  %s\n", describeRule(entry.Access.Rule()))
	fmt.Println("\nwaiting; ctrl-c to stop")

	// Going away is what takes the path down: the node holds the mount for exactly as long as this
	// connection lasts.
	gone := make(chan struct{})
	go func() {
		defer close(gone)
		_, _ = io.Copy(io.Discard, conn)
	}()

	select {
	case <-ctx.Done():
	case <-gone:
	}
	fmt.Printf("\n%s is gone\n", at)
	return nil
}

func runRemove(at string) error {
	at, err := ns.Clean(at)
	if err != nil {
		return err
	}

	cfg, err := conf.Load(reading())
	if err != nil {
		return err
	}
	defer cfg.Close()

	if declares(cfg, at) {
		return fmt.Errorf("%s declares %s, so edit that to take it down", where(cfg), at)
	}

	store, err := made.Load()
	if err != nil {
		return err
	}
	file, err := made.Path()
	if err != nil {
		return err
	}

	had, err := store.Remove(at)
	if err != nil {
		return err
	}
	if !had {
		return fmt.Errorf("%s says nothing about %s", file, at)
	}

	// Out of the file and off the node, in that order. A path removed from the list and still
	// answering is the one failure that matters here: somebody stops sharing something and it goes
	// on being shared, and being told so is the difference between fixing it and not knowing.
	fmt.Printf("%s is out of %s\n", at, file)
	switch err := unmounted(at); {
	case err == nil:
	case errors.Is(err, errNoNode):
	default:
		fmt.Printf("  the node running here goes on serving it until it restarts: %v\n", err)
	}
	return nil
}

// declares reports whether the config itself says something about a path, exactly rather than by
// prefix: a rule at /work does not make /work/notes somebody else's to put up.
func declares(cfg *conf.Config, at string) bool {
	for _, m := range cfg.Mounts.All() {
		if m.Path == at && m.Source == ns.Configured {
			return true
		}
	}
	return false
}

// where names the config, or what stands in for it when there is no file.
func where(cfg *conf.Config) string {
	if cfg.Path == "" {
		return "the built-in defaults"
	}
	return cfg.Path
}

// settings turns the three set-flags into a declaration.
//
// Three rather than one because a declaration says exactly three kinds of thing, and the flags map
// one for one onto them.
func settings(text, on, lists []string) (made.Settings, error) {
	out := made.Settings{}

	for _, item := range text {
		key, value, err := split(item, "--set")
		if err != nil {
			return nil, err
		}
		out[key] = value
	}

	for _, item := range on {
		key, value, found := strings.Cut(item, "=")
		if key = strings.TrimSpace(key); key == "" {
			return nil, fmt.Errorf("--flag %q needs a key", item)
		}
		switch {
		case !found, value == "true":
			out[key] = true
		case value == "false":
			out[key] = false
		default:
			return nil, fmt.Errorf("--flag %s=%s: a flag is on or off, so leave it bare or write =false", key, value)
		}
	}

	for _, item := range lists {
		key, value, err := split(item, "--list")
		if err != nil {
			return nil, err
		}
		out[key] = names(value)
	}
	return out, nil
}

// split reads key=value, which is how a setting is given a name and a value on one flag.
func split(item, flag string) (string, string, error) {
	key, value, found := strings.Cut(item, "=")
	key = strings.TrimSpace(key)
	if !found || key == "" {
		return "", "", fmt.Errorf("%s %q: write it as key=value", flag, item)
	}
	return key, value, nil
}

// admitting reads --access and --visible, in the same words the config uses.
func admitting(access, visible string) (made.Access, error) {
	var out made.Access

	switch access {
	case "":
		// Refused rather than defaulted. A setting may name a command to run, so a namespace put
		// up without saying who may reach it is one anybody paired could run.
		return out, errors.New("this needs --access: paired, trusted, anyone, or who may reach it by name")
	case "paired":
		out.Paired = true
	case "trusted":
		out.Trusted = true
	case "anyone":
		out.Anyone = true
	default:
		if out.Named = names(access); len(out.Named) == 0 {
			return out, fmt.Errorf("--access %q names nobody", access)
		}
	}

	switch visible {
	case "":
	case "paired", "anyone":
		out.VisiblePaired = true
	case "trusted":
		out.VisibleTrusted = true
	default:
		if out.Visible = names(visible); len(out.Visible) == 0 {
			return out, fmt.Errorf("--visible %q names nobody", visible)
		}
	}
	return out, nil
}

// names reads a comma-separated list, dropping what is only whitespace.
func names(text string) []string {
	var out []string
	for _, item := range strings.Split(text, ",") {
		if item = strings.TrimSpace(item); item != "" {
			out = append(out, item)
		}
	}
	return out
}

// unmounted asks the node running here to stop serving a path, and says nothing when there is no
// node: a path nobody is serving is already down.
func unmounted(at string) error {
	conn, err := asking()
	if err != nil {
		return err
	}
	defer conn.Close()

	if _, err := fmt.Fprintf(conn, "unmount %s\n", at); err != nil {
		return err
	}

	said, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		return fmt.Errorf("asking this node to drop %s: %w", at, err)
	}
	if what, why, _ := strings.Cut(strings.TrimSpace(said), " "); what != "ok" {
		return errors.New(why)
	}
	return nil
}

// errNoNode is what this machine says when nothing is serving on it.
var errNoNode = errors.New("nothing is serving on this device: start `drop serve` first")

// asking connects to the node already running here.
func asking() (net.Conn, error) {
	path, err := castSocket()
	if err != nil {
		return nil, err
	}
	conn, err := net.Dial("unix", path)
	if err != nil {
		return nil, errNoNode
	}
	return conn, nil
}

// tell hands a declaration to that node and waits to be told it is up.
//
// One JSON object rather than fields with spaces between them, because a declaration has whatever
// keys the archetype reads. Both ends are the same binary, so there is nothing to negotiate.
func tell(conn net.Conn, line made.Line) (*bufio.Reader, error) {
	raw, err := json.Marshal(line)
	if err != nil {
		return nil, fmt.Errorf("writing the declaration for %s: %w", line.Path, err)
	}
	if _, err := fmt.Fprintf(conn, "mount %s\n", raw); err != nil {
		return nil, err
	}

	reading := bufio.NewReader(conn)
	said, err := reading.ReadString('\n')
	if err != nil {
		return nil, fmt.Errorf("asking this node for %s: %w", line.Path, err)
	}
	if what, why, _ := strings.Cut(strings.TrimSpace(said), " "); what != "ok" {
		return nil, fmt.Errorf("this node will not serve %s: %s", line.Path, why)
	}
	return reading, nil
}
