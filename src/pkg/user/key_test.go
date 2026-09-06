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
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socket := filepath.Join(dir, "agent.sock")
	var listen net.ListenConfig
	listener, err := listen.Listen(t.Context(), "unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })

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

func TestAgentRequestsAreBounded(t *testing.T) {
	_, secret, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	public, err := ssh.NewPublicKey(secret.Public())
	if err != nil {
		t.Fatal(err)
	}

	dir, err := os.MkdirTemp("/tmp", "drop-stuck-agent-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socket := filepath.Join(dir, "agent.sock")
	var listen net.ListenConfig
	listener, err := listen.Listen(t.Context(), "unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })

	stop := make(chan struct{})
	t.Cleanup(func() { close(stop) })
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				<-stop
				_ = conn.Close()
			}()
		}
	}()

	const within = 50 * time.Millisecond
	started := time.Now()
	if _, err := findAgent(public, socket, within); err == nil {
		t.Fatal("an agent that never listed its keys was waited on forever")
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("listing keys took %s", elapsed)
	}

	started = time.Now()
	signer := agentSigner{key: public, socket: socket, within: within}
	if _, err := signer.Sign(nil, []byte("message")); err == nil {
		t.Fatal("an agent that never signed was waited on forever")
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("signing took %s", elapsed)
	}
}
