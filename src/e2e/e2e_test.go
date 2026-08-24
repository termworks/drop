//go:build e2e

// Package e2e drives the command line the way a person does: two real nodes, on this machine, over
// QUIC, saying things to each other.
//
// Nothing here reaches inside the program. Every check is a command run and an outcome looked at,
// because what is being tested is whether drop works, not whether its packages compile together.
package e2e

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// node is one drop installation: its own identity, address book, conversations and config.
type node struct {
	t    *testing.T
	name string
	home string
	port string
	// profile makes this a second person under the same home, reached with $DROP_PROFILE. Its
	// port is derived from the name, so it does not need one of its own.
	profile string
}

// binary is the drop under test, built once for the whole run.
var binary = sync.OnceValues(func() (string, error) {
	out, err := filepath.Abs(filepath.Join(os.TempDir(), "drop-e2e", "drop"))
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return "", err
	}

	build := exec.Command("go", "build", "-o", out, "../")
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	if said, err := build.CombinedOutput(); err != nil {
		return "", fmt.Errorf("building drop: %v\n%s", err, said)
	}
	return out, nil
})

func newNode(t *testing.T, name, port string) *node {
	t.Helper()

	home := t.TempDir()
	for _, dir := range []string{"config", "data", "inbox"} {
		if err := os.MkdirAll(filepath.Join(home, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return &node{t: t, name: name, home: home, port: port}
}

func (n *node) env() []string {
	if n.profile != "" {
		return append(os.Environ(),
			"DROP_PROFILE="+n.profile,
			"XDG_CONFIG_HOME="+filepath.Join(n.home, "config"),
			"XDG_DATA_HOME="+filepath.Join(n.home, "data"),
			"DROP_OPENER=/bin/true",
		)
	}

	return append(os.Environ(),
		"DROP_NAME="+n.name,
		"DROP_PORT="+n.port,
		"XDG_CONFIG_HOME="+filepath.Join(n.home, "config"),
		"XDG_DATA_HOME="+filepath.Join(n.home, "data"),
		// A link that arrives must not open a browser on the machine running the tests.
		"DROP_OPENER=/bin/true",
	)
}

func (n *node) inbox() string { return filepath.Join(n.home, "inbox") }

// serves writes this node's configuration.
func (n *node) serves(config string) {
	n.t.Helper()

	dir := filepath.Join(n.home, "config", "drop")
	if n.profile != "" {
		dir = filepath.Join(dir, "profiles", n.profile)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		n.t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "init.lua"), []byte(config), 0o644); err != nil {
		n.t.Fatal(err)
	}
}

// run executes a drop command and waits for it, returning everything it printed.
func (n *node) run(args ...string) (string, error) {
	n.t.Helper()

	ctx, stop := context.WithTimeout(context.Background(), 90*time.Second)
	defer stop()

	return n.runIn(ctx, "", args...)
}

// runIn is run with something on standard input.
func (n *node) runIn(ctx context.Context, stdin string, args ...string) (string, error) {
	n.t.Helper()

	drop, err := binary()
	if err != nil {
		n.t.Fatal(err)
	}

	cmd := exec.CommandContext(ctx, drop, args...)
	cmd.Env = n.env()
	cmd.Dir = n.home
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}

	said, err := cmd.CombinedOutput()
	return string(said), err
}

// must runs a command and fails the test if it does not succeed.
func (n *node) must(args ...string) string {
	n.t.Helper()

	said, err := n.run(args...)
	if err != nil {
		n.t.Fatalf("%s: drop %s failed: %v\n%s", n.name, strings.Join(args, " "), err, said)
	}
	return said
}

// background starts a command that is meant to keep running, and hands back what it prints.
func (n *node) background(args ...string) (*exec.Cmd, *strings.Builder, func()) {
	n.t.Helper()

	drop, err := binary()
	if err != nil {
		n.t.Fatal(err)
	}

	ctx, stop := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, drop, args...)
	cmd.Env = n.env()
	cmd.Dir = n.home

	pipe, err := cmd.StdoutPipe()
	if err != nil {
		n.t.Fatal(err)
	}
	cmd.Stderr = cmd.Stdout

	said := &strings.Builder{}
	var mu sync.Mutex

	if err := cmd.Start(); err != nil {
		stop()
		n.t.Fatalf("%s: starting drop %s: %v", n.name, strings.Join(args, " "), err)
	}

	go func() {
		reader := bufio.NewReader(pipe)
		for {
			line, err := reader.ReadString('\n')
			mu.Lock()
			said.WriteString(line)
			mu.Unlock()
			if err != nil {
				return
			}
		}
	}()

	return cmd, said, func() {
		stop()
		_ = cmd.Wait()
	}
}

// waitFor watches something until it is true, so a test never sleeps longer than it has to.
func waitFor(t *testing.T, what string, within time.Duration, ready func() bool) {
	t.Helper()

	until := time.Now().Add(within)
	for time.Now().Before(until) {
		if ready() {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// pair links two nodes the way the documentation says to: one shows a ticket, the other takes it.
func pair(t *testing.T, showing, taking *node) { pairing(t, showing, taking) }

// pairing is the same, with whatever flags the far side should use.
func pairing(t *testing.T, showing, taking *node, flags ...string) {
	t.Helper()

	_, said, stop := showing.background("pair", "--code", "e2e-test-code", "--wait", "3m")
	defer stop()

	var ticket string
	waitFor(t, "a ticket", 30*time.Second, func() bool {
		ticket = ticketIn(said.String())
		return ticket != ""
	})

	out := taking.must(append([]string{"pair", ticket}, flags...)...)
	if !strings.Contains(out, "reach the other") {
		t.Fatalf("pairing did not finish:\n%s", out)
	}

	waitFor(t, "the offer to close", 30*time.Second, func() bool {
		return strings.Contains(said.String(), "either device")
	})
}

// ticketIn finds the ticket in what `drop pair` printed.
func ticketIn(said string) string {
	for _, line := range strings.Split(said, "\n") {
		if _, rest, found := strings.Cut(line, "ticket:"); found {
			return strings.TrimSpace(rest)
		}
	}
	return ""
}

// backgroundWriting starts a command whose standard input the test keeps writing to, which is what
// a cast is: something that goes on producing output until whatever is producing it stops.
func (n *node) backgroundWriting(args ...string) (io.WriteCloser, *strings.Builder, func()) {
	n.t.Helper()

	drop, err := binary()
	if err != nil {
		n.t.Fatal(err)
	}

	ctx, stop := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, drop, args...)
	cmd.Env = n.env()
	cmd.Dir = n.home

	into, err := cmd.StdinPipe()
	if err != nil {
		stop()
		n.t.Fatal(err)
	}
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		stop()
		n.t.Fatal(err)
	}
	cmd.Stderr = cmd.Stdout

	said := &strings.Builder{}
	if err := cmd.Start(); err != nil {
		stop()
		n.t.Fatalf("%s: starting drop %s: %v", n.name, strings.Join(args, " "), err)
	}

	go func() {
		reader := bufio.NewReader(pipe)
		for {
			line, err := reader.ReadString('\n')
			said.WriteString(line)
			if err != nil {
				return
			}
		}
	}()

	return into, said, func() {
		_ = into.Close()
		stop()
		_ = cmd.Wait()
	}
}
