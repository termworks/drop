package cmd

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/bresilla/drop/src/pkg/passwd"
)

func newPasswdCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "passwd",
		Short: "Hash a password, to guard a path with",
		Long: "passwd reads a password and prints the hash to put in your config.\n\n" +
			"The config holds the hash, never the password: a config gets copied into dotfile\n" +
			"repositories and backups, and a hash there is not a way in.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			secret, err := readPassword()
			if err != nil {
				return err
			}

			hash, err := passwd.Hash(secret)
			if err != nil {
				return err
			}

			// The hash alone on stdout, so it can be piped into a config without editing.
			fmt.Println(hash)

			fmt.Fprintf(os.Stderr, "\ndrop.mount(\"/somewhere\", {\n  type   = \"files\",\n"+
				"  access = { password = \"%s\" },\n})\n", hash)
			return nil
		},
	}
}

// readPassword takes it from a terminal without echoing, or from a pipe when there is no terminal —
// which is what makes this scriptable without ever putting the word in a shell history.
func readPassword() (string, error) {
	fd := int(os.Stdin.Fd())

	if !term.IsTerminal(fd) {
		line, err := bufio.NewReader(os.Stdin).ReadString('\n')
		if err != nil && line == "" {
			return "", fmt.Errorf("reading the password: %w", err)
		}
		return strings.TrimRight(line, "\r\n"), nil
	}

	fmt.Fprint(os.Stderr, "password: ")
	first, err := term.ReadPassword(fd)
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", fmt.Errorf("reading the password: %w", err)
	}

	fmt.Fprint(os.Stderr, "again: ")
	second, err := term.ReadPassword(fd)
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", fmt.Errorf("reading the password: %w", err)
	}

	if string(first) != string(second) {
		return "", fmt.Errorf("the two did not match")
	}
	return string(first), nil
}
