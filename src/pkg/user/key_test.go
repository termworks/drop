package user

import (
	"crypto/ed25519"
	"crypto/rand"
	"net"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

func TestOneUserKeyIsMadeForConcurrentCallers(t *testing.T) {
	where := filepath.Join(t.TempDir(), "user")
	got := [8]ssh.Signer{}

	var wg sync.WaitGroup
	for i := range got {
		wg.Add(1)
		go func() {
			defer wg.Done()
			signer, err := makeKey(where)
			if err != nil {
				t.Errorf("makeKey(): %v", err)
				return
			}
			got[i] = signer
		}()
	}
	wg.Wait()

	if got[0] == nil {
		t.Fatal("the first caller got no key")
	}
	want := got[0].PublicKey().Marshal()
	for i, signer := range got {
		if signer == nil || string(signer.PublicKey().Marshal()) != string(want) {
			t.Fatalf("caller %d got a different key", i)
		}
	}

	raw, err := os.ReadFile(where)
	if err != nil {
		t.Fatal(err)
	}
	stored, err := ssh.ParsePrivateKey(raw)
	if err != nil {
		t.Fatal(err)
	}
	if string(stored.PublicKey().Marshal()) != string(want) {
		t.Fatal("the stored key differs from the returned key")
	}
}

func TestAgentConnectionsCloseAfterUse(t *testing.T) {
	_, secret, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	keyring := agent.NewKeyring()
	if err := keyring.Add(agent.AddedKey{PrivateKey: secret}); err != nil {
		t.Fatal(err)
	}
	public, err := ssh.NewPublicKey(secret.Public())
	if err != nil {
		t.Fatal(err)
	}

	dir, err := os.MkdirTemp("/tmp", "drop-agent-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	socket := filepath.Join(dir, "agent.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })

	var active atomic.Int32
	closed := make(chan struct{}, 2)
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			active.Add(1)
			go func() {
				_ = agent.ServeAgent(keyring, conn)
				_ = conn.Close()
				active.Add(-1)
				closed <- struct{}{}
			}()
		}
	}()

	t.Setenv("SSH_AUTH_SOCK", socket)
	signer, err := fromAgent(public)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Signature(signer, []byte("message")); err != nil {
		t.Fatal(err)
	}

	for range 2 {
		select {
		case <-closed:
		case <-time.After(time.Second):
			t.Fatalf("%d agent connections remain open", active.Load())
		}
	}
	if active.Load() != 0 {
		t.Fatalf("%d agent connections remain open", active.Load())
	}
}
