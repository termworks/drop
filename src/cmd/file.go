package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/bresilla/drop/src/pkg/arch/files"
	"github.com/bresilla/drop/src/pkg/conf"
	"github.com/bresilla/drop/src/pkg/ns"
)

// The verbs for a directory somebody serves: what is in it, and what may be moved in and out.

func newFileCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "file",
		Short: "What is inside a files namespace",
		Long: "A files namespace is a directory the far end walks for you. The address names the\n" +
			"namespace, and what follows it is a name on that machine, spelt however that\n" +
			"filesystem spells it.\n\n" +
			"  drop file ls orin:/work/deep\n" +
			"  drop file get orin:/work/report.pdf\n" +
			"  drop file put orin:/work notes.md\n\n" +
			"An address that is this machine lists this machine's own namespace.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
	}

	cmd.AddCommand(
		newFileListCmd(),
		newFileGetCmd(),
		newFilePutCmd(),
		newFileRemoveCmd(),
		newFileMkdirCmd(),
		newFileMoveCmd(),
	)
	return cmd
}

func newFileListCmd() *cobra.Command {
	var wait time.Duration

	cmd := &cobra.Command{
		Use:   "ls <address>",
		Short: "List what is in a directory somebody serves",
		Long: "ls walks into the files namespace the address lands in and lists one directory of\n" +
			"it, going as deep as the address does.\n\n" +
			"With an address that is this machine it reads the config and lists the directory\n" +
			"that namespace serves, without a wire in between.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return listFiles(cmd.Context(), args[0], wait)
		},
	}

	cmd.Flags().DurationVarP(&wait, "wait", "w", 30*time.Second, "how long to spend reaching the machine")

	return cmd
}

func newFileGetCmd() *cobra.Command {
	var wait time.Duration

	cmd := &cobra.Command{
		Use:   "get <address> [into]",
		Short: "Copy a file out of a directory somebody shares",
		Long: "get reads one file out of a directory another machine serves.\n\n" +
			"With no destination it lands here under its own name. A destination that is a\n" +
			"directory takes it under its own name too; anything else is the file to write.\n\n" +
			"  drop file get orin:/work/report.pdf\n" +
			"  drop file get bob:laptop:/work/deep/inner.txt ~/here.txt",
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := notHere(args[0], "copy a file out of"); err != nil {
				return err
			}
			into := ""
			if len(args) == 2 {
				into = args[1]
			}
			return getFrom(cmd.Context(), args[0], into, wait)
		},
	}

	cmd.Flags().DurationVarP(&wait, "wait", "w", 90*time.Second, "how long to spend reaching the machine")

	return cmd
}

func newFilePutCmd() *cobra.Command {
	var (
		as   string
		wait time.Duration
	)

	cmd := &cobra.Command{
		Use:   "put <address> <file>...",
		Short: "Copy files into a directory somebody shares",
		Long: "put writes files into a directory another machine serves, if that directory takes\n" +
			"anything back.\n\n" +
			"  drop file put orin:/work report.pdf        one file at the top of it\n" +
			"  drop file put orin:/work/deep a b c        into a directory inside it\n" +
			"  drop file put orin:/work - --as note.txt   and - is standard input",
		Args: cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := notHere(args[0], "copy files into"); err != nil {
				return err
			}
			return putInto(cmd.Context(), args[0], args[1:], as, wait)
		},
	}

	cmd.Flags().StringVar(&as, "as", "stdin", "the name to give standard input")
	cmd.Flags().DurationVarP(&wait, "wait", "w", 90*time.Second, "how long to spend reaching the machine")

	return cmd
}

func newFileRemoveCmd() *cobra.Command {
	var wait time.Duration

	cmd := &cobra.Command{
		Use:   "rm <address>",
		Short: "Remove a file from a directory somebody shares",
		Long:  "rm removes one file, or one directory that is already empty.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := notHere(args[0], "remove anything from"); err != nil {
				return err
			}
			return changeThere(cmd.Context(), args[0], wait, func(w *walking) error {
				return w.Remove(w.rest)
			}, "removed")
		},
	}

	cmd.Flags().DurationVarP(&wait, "wait", "w", 90*time.Second, "how long to spend reaching the machine")

	return cmd
}

func newFileMkdirCmd() *cobra.Command {
	var wait time.Duration

	cmd := &cobra.Command{
		Use:   "mkdir <address>",
		Short: "Make a directory inside one somebody shares",
		Long:  "mkdir makes one directory. Its parent has to be there already.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := notHere(args[0], "make a directory in"); err != nil {
				return err
			}
			return changeThere(cmd.Context(), args[0], wait, func(w *walking) error {
				return w.Mkdir(w.rest)
			}, "made")
		},
	}

	cmd.Flags().DurationVarP(&wait, "wait", "w", 90*time.Second, "how long to spend reaching the machine")

	return cmd
}

func newFileMoveCmd() *cobra.Command {
	var wait time.Duration

	cmd := &cobra.Command{
		Use:   "mv <address> <to>",
		Short: "Move something inside a directory somebody shares",
		Long: "mv renames something without it ever leaving that machine.\n\n" +
			"The destination is named from the top of the same namespace, so\n" +
			"`drop file mv orin:/work/deep/old.txt deep/new.txt` leaves it where it is.",
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := notHere(args[0], "move anything in"); err != nil {
				return err
			}
			to := strings.Trim(args[1], "/")
			return changeThere(cmd.Context(), args[0], wait, func(w *walking) error {
				return w.Move(w.rest, to)
			}, "moved")
		},
	}

	cmd.Flags().DurationVarP(&wait, "wait", "w", 90*time.Second, "how long to spend reaching the machine")

	return cmd
}

// notHere refuses an address that is this machine, for a verb that only means something over a
// wire. Your own files are yours already, and the shell is how you move them.
func notHere(target, doing string) error {
	at, _, err := splitAddress(target)
	if err != nil {
		return err
	}
	if at.Here {
		return fmt.Errorf("%s is this machine: drop will not %s its own namespace, use the shell", target, doing)
	}
	return nil
}

// listFiles lists a directory on another machine, or one of this machine's own.
func listFiles(ctx context.Context, target string, wait time.Duration) error {
	at, under, err := splitAddress(target)
	if err != nil {
		return err
	}
	if at.Here {
		return listOwnFiles(under)
	}

	w, err := walk(ctx, target, wait)
	if err != nil {
		return err
	}
	defer w.stop()

	return listInside(w.Browsing, w.entry.ID, target, w.rest)
}

// listOwnFiles lists one of this machine's own directories, read out of the config rather than
// asked for over a wire.
func listOwnFiles(under string) error {
	cfg, err := conf.Load(reading())
	if err != nil {
		return err
	}
	defer cfg.Close()

	mount, rest, ok := cfg.Mounts.Lookup(under)
	switch {
	case !ok:
		return fmt.Errorf("this machine serves nothing at %s: `drop path ls` says what it does serve", under)
	case mount.Archetype != "files":
		return fmt.Errorf("%s is a %s namespace here, not a directory to walk", mount.Path, kindOf(mount.Archetype))
	}
	dir, ok := mount.Config.(files.Config)
	if !ok || dir.Dir == "" {
		return fmt.Errorf("%s has no directory behind it", mount.Path)
	}

	inside := filepath.Join(dir.Dir, filepath.FromSlash(strings.Trim(rest, "/")))
	items, err := os.ReadDir(inside)
	if err != nil {
		return fmt.Errorf("reading %s: %w", inside, err)
	}

	where := ns.Address{Path: under, Here: true}
	if len(items) == 0 {
		fmt.Printf("\n%s is empty\n\n", where)
		return nil
	}

	// Directories first, then names, which is the order a person reads a directory in.
	sort.Slice(items, func(i, j int) bool {
		if items[i].IsDir() != items[j].IsDir() {
			return items[i].IsDir()
		}
		return items[i].Name() < items[j].Name()
	})

	names := make([]string, 0, len(items))
	for _, item := range items {
		names = append(names, ownName(item))
	}
	width := widest(0, names)

	fmt.Printf("\n%s  %s\n\n", where, inside)
	for _, item := range items {
		stat, err := item.Info()
		if err != nil {
			continue
		}
		size := ""
		if !item.IsDir() {
			size = bytes(stat.Size())
		}
		fmt.Printf("  %-*s  %10s  %s\n", width, ownName(item), size, stat.ModTime().Format("2006-01-02 15:04"))
	}
	fmt.Println()
	return nil
}

// ownName marks a directory, so a listing needs no column to say which is which.
func ownName(item os.DirEntry) string {
	if item.IsDir() {
		return item.Name() + "/"
	}
	return item.Name()
}
