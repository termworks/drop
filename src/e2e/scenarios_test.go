//go:build e2e

package e2e

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The whole command line, between two nodes, in the order a person meets it: pair, look at what the
// other one shares, then use each kind of namespace there is.
//
// One test rather than several, because every step needs the pairing the step before it made, and a
// suite that pairs four times to check four things spends its life pairing.
func TestTwoNodes(t *testing.T) {
	alpha := newNode(t, "alpha", "47801")
	beta := newNode(t, "beta", "47802")

	beta.serves(`
local drop = require("drop")

drop.mount("/chat",  { type = "chat",  access = "paired" })
drop.mount("/inbox", { type = "files", access = "paired", dir = "` + beta.inbox() + `" })
drop.mount("/open",  { type = "link",  access = "paired" })
drop.mount("/ticks", { type = "stream", access = "paired",
  command = "sh -c 'for i in 1 2 3; do echo tick $i; sleep 1; done'" })
drop.mount("/term",  { type = "tty",   access = "paired", input = true, shell = "/bin/sh" })
`)

	alpha.serves(`
local drop = require("drop")

drop.mount("/chat",  { type = "chat",  access = "paired" })
drop.mount("/inbox", { type = "files", access = "paired", dir = "` + alpha.inbox() + `" })
`)

	t.Run("a node knows its own identity", func(t *testing.T) {
		if id := strings.TrimSpace(alpha.must("id")); len(id) != 64 {
			t.Fatalf("id = %q, want 64 hex characters", id)
		}
	})

	t.Run("a node lists what it serves", func(t *testing.T) {
		out := beta.must("ns")
		for _, path := range []string{"/chat", "/inbox", "/open", "/ticks", "/term"} {
			if !strings.Contains(out, path) {
				t.Errorf("%s is not served:\n%s", path, out)
			}
		}
		if strings.Contains(out, "nobody") {
			t.Errorf("something is served to nobody:\n%s", out)
		}
	})

	// Both stay up for the rest of it: everything below needs somebody listening on the far end.
	_, betaSaid, stopBeta := beta.background("serve")
	defer stopBeta()
	_, alphaSaid, stopAlpha := alpha.background("serve")

	defer stopAlpha()

	waitFor(t, "both nodes to be ready", 30*time.Second, func() bool {
		return strings.Contains(betaSaid.String(), "ready") && strings.Contains(alphaSaid.String(), "ready")
	})

	t.Run("two devices pair", func(t *testing.T) {
		pair(t, beta, alpha)

		if out := alpha.must("peers"); !strings.Contains(out, "beta") {
			t.Errorf("alpha does not know beta:\n%s", out)
		}
		if out := beta.must("peers"); !strings.Contains(out, "alpha") {
			t.Errorf("beta does not know alpha:\n%s", out)
		}
	})

	t.Run("one device asks what the other shares", func(t *testing.T) {
		out := alpha.must("ls", "beta")
		for _, path := range []string{"/chat", "/inbox", "/open", "/ticks", "/term"} {
			if !strings.Contains(out, path) {
				t.Errorf("beta did not offer %s:\n%s", path, out)
			}
		}
	})

	t.Run("a message is sent and arrives", func(t *testing.T) {
		alpha.must("to", "beta/chat", "the eagle has landed")

		waitFor(t, "the message to arrive", 30*time.Second, func() bool {
			return strings.Contains(beta.must("log", "alpha"), "the eagle has landed")
		})
	})

	t.Run("a reply comes back the other way", func(t *testing.T) {
		beta.must("to", "alpha/chat", "roger that")

		waitFor(t, "the reply to arrive", 30*time.Second, func() bool {
			return strings.Contains(alpha.must("log", "beta"), "roger that")
		})
	})

	t.Run("a file is sent and lands", func(t *testing.T) {
		file := filepath.Join(alpha.home, "report.txt")
		if err := os.WriteFile(file, []byte("forty two\n"), 0o644); err != nil {
			t.Fatal(err)
		}

		alpha.must("to", "beta/inbox", file)

		landed := filepath.Join(beta.inbox(), "report.txt")
		waitFor(t, "the file to land", 30*time.Second, func() bool {
			_, err := os.Stat(landed)
			return err == nil
		})

		got, err := os.ReadFile(landed)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != "forty two\n" {
			t.Errorf("the file arrived as %q", got)
		}
	})

	t.Run("a file goes back the other way", func(t *testing.T) {
		file := filepath.Join(beta.home, "answer.txt")
		if err := os.WriteFile(file, []byte("received\n"), 0o644); err != nil {
			t.Fatal(err)
		}

		beta.must("to", "alpha/inbox", file)

		waitFor(t, "the file to land", 30*time.Second, func() bool {
			_, err := os.Stat(filepath.Join(alpha.inbox(), "answer.txt"))
			return err == nil
		})
	})

	t.Run("standard input is sent as a file", func(t *testing.T) {
		ctx, stop := context.WithTimeout(context.Background(), 60*time.Second)
		defer stop()

		if out, err := alpha.runIn(ctx, "piped in\n", "to", "beta/inbox", "-", "--as", "piped.txt"); err != nil {
			t.Fatalf("sending standard input: %v\n%s", err, out)
		}

		landed := filepath.Join(beta.inbox(), "piped.txt")
		waitFor(t, "the piped file to land", 30*time.Second, func() bool {
			_, err := os.Stat(landed)
			return err == nil
		})

		got, err := os.ReadFile(landed)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != "piped in\n" {
			t.Errorf("the piped file arrived as %q", got)
		}
	})

	t.Run("a link is sent and recorded", func(t *testing.T) {
		alpha.must("to", "beta/open", "https://iroh.computer/docs")

		waitFor(t, "the link to arrive", 30*time.Second, func() bool {
			return strings.Contains(beta.must("log", "alpha"), "https://iroh.computer/docs")
		})
	})

	t.Run("a stream is read until it ends", func(t *testing.T) {
		out := alpha.must("to", "beta/ticks")

		for _, want := range []string{"tick 1", "tick 2", "tick 3"} {
			if !strings.Contains(out, want) {
				t.Errorf("the stream did not carry %q:\n%s", want, out)
			}
		}
		// A stream namespace is a command with no terminal, so its bare newlines have to be
		// translated on the way out or every line starts further right than the last.
		if strings.Contains(out, "\n      tick") {
			t.Errorf("the stream came out as a staircase:\n%q", out)
		}
	})

	t.Run("a terminal is opened and typed into", func(t *testing.T) {
		ctx, stop := context.WithTimeout(context.Background(), 60*time.Second)
		defer stop()

		out, err := alpha.runIn(ctx, "echo marker-from-the-far-side\nexit\n", "to", "beta/term")
		if err != nil {
			t.Fatalf("opening a terminal: %v\n%s", err, out)
		}
		if !strings.Contains(out, "marker-from-the-far-side") {
			t.Errorf("the shell did not run what was typed into it:\n%s", out)
		}
	})

	t.Run("the conversation remembers all of it", func(t *testing.T) {
		out := alpha.must("log", "beta")

		for _, want := range []string{"the eagle has landed", "roger that", "report.txt"} {
			if !strings.Contains(out, want) {
				t.Errorf("the log has no %q:\n%s", want, out)
			}
		}
	})

	t.Run("a device can be forgotten", func(t *testing.T) {
		alpha.must("peers", "rm", "beta")

		if out := alpha.must("peers"); strings.Contains(out, "beta") {
			t.Errorf("beta survived being forgotten:\n%s", out)
		}
	})
}
