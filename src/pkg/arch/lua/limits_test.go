package lua

import (
	"context"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bresilla/drop/src/pkg/arch"
	"github.com/bresilla/drop/src/pkg/ns"
	"github.com/bresilla/drop/src/pkg/wire"
)

func TestLuaSessionsHaveAProcessWideLimit(t *testing.T) {
	p := written(t, `
		drop.archetype{
			name  = "waiter",
			read  = function(d) return {} end,
			note  = function(c) return {} end,
			serve = function(s, c)
				s:write("ready")
				s:read()
			end,
		}
	`)
	p.limits = newResourceLimits(1, 8, 2)

	first, firstClient, firstDone := opened(t, p, nil)
	if _, body := said(t, first); string(body) != "ready" {
		t.Fatalf("the first session said %q", body)
	}

	_, refusedClient, refused := opened(t, p, map[string]any{})
	if err := ended(t, refused, time.Second); err == nil || !strings.Contains(err.Error(), "1 Lua sessions") {
		t.Fatalf("the session above the process limit ended with %v", err)
	}
	_ = refusedClient.Close()

	_ = firstClient.Close()
	if err := ended(t, firstDone, time.Second); err != nil {
		t.Fatalf("the first session ended with %v", err)
	}

	after, afterClient, afterDone := opened(t, p, map[string]any{})
	if _, body := said(t, after); string(body) != "ready" {
		t.Fatalf("the session after capacity returned said %q", body)
	}
	_ = afterClient.Close()
	if err := ended(t, afterDone, time.Second); err != nil {
		t.Fatalf("the session after capacity returned ended with %v", err)
	}
}

func TestLuaFilesHaveAProcessWideLimit(t *testing.T) {
	p := written(t, `
		drop.archetype{
			name  = "holder",
			read  = function(d) return {} end,
			note  = function(c) return {} end,
			serve = function(s, c)
				local f = s:open(s:mine("held"), "w")
				s:write("open")
				s:read()
				f:close()
			end,
		}
	`)
	p.limits = newResourceLimits(3, 1, 2)

	first, firstClient, firstDone := opened(t, p, nil)
	if _, body := said(t, first); string(body) != "open" {
		t.Fatalf("the first session said %q", body)
	}

	_, refusedClient, refused := opened(t, p, nil)
	if err := ended(t, refused, time.Second); err == nil || !strings.Contains(err.Error(), "1 plugin files") {
		t.Fatalf("the file above the process limit ended with %v", err)
	}
	_ = refusedClient.Close()

	_ = firstClient.Close()
	if err := ended(t, firstDone, time.Second); err != nil {
		t.Fatalf("the first session ended with %v", err)
	}

	after, afterClient, afterDone := opened(t, p, nil)
	if _, body := said(t, after); string(body) != "open" {
		t.Fatalf("the session after file capacity returned said %q", body)
	}
	_ = afterClient.Close()
	if err := ended(t, afterDone, time.Second); err != nil {
		t.Fatalf("the session after file capacity returned ended with %v", err)
	}
}

func TestLuaProcessesHaveAProcessWideLimit(t *testing.T) {
	p := written(t, `
		drop.archetype{
			name  = "runner",
			read  = function(d) return { hold = d.hold } end,
			note  = function(c) return {} end,
			serve = function(s, c)
				if c and c.hold then
					s:run{ "/bin/sh", "-c", ": > up; sleep 30" }
				else
					s:run{ "/bin/true" }
					s:write("ran")
				end
			end,
		}
	`)
	p.limits = newResourceLimits(3, 8, 1)

	ctx, cancel := context.WithCancel(t.Context())
	firstClient, firstServer := net.Pipe()
	firstDone := make(chan error, 1)
	go func() {
		defer func() { _ = firstServer.Close() }()
		firstDone <- p.Serve(ctx, arch.Session{
			Path:   "/thing",
			Config: map[string]any{"hold": true},
			Who:    ns.Caller{ID: "aaaa", Name: "laptop", Paired: true},
			Conn:   wire.NewConn(firstServer),
			Stream: deadlined{firstServer},
		})
	}()
	defer func() { _ = firstClient.Close() }()

	until(t, filepath.Join(p.keeps, p.name, slug("/thing"), "up"))

	_, refusedClient, refused := opened(t, p, nil)
	if err := ended(t, refused, time.Second); err == nil || !strings.Contains(err.Error(), "1 plugin processes") {
		t.Fatalf("the process above the process limit ended with %v", err)
	}
	_ = refusedClient.Close()

	cancel()
	if err := ended(t, firstDone, 10*time.Second); err == nil {
		t.Fatal("the cancelled process session ended without an error")
	}

	after, afterClient, afterDone := opened(t, p, nil)
	if _, body := said(t, after); string(body) != "ran" {
		t.Fatalf("the session after process capacity returned said %q", body)
	}
	_ = afterClient.Close()
	if err := ended(t, afterDone, time.Second); err != nil {
		t.Fatalf("the session after process capacity returned ended with %v", err)
	}
}
