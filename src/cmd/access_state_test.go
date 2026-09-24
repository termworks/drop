package cmd

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/bresilla/drop/src/pkg/asked"
	"github.com/bresilla/drop/src/pkg/grant"
	"github.com/bresilla/drop/src/pkg/node"
)

func TestEquivalentPathsAnswerTheSameRequest(t *testing.T) {
	for _, allowed := range []bool{true, false} {
		t.Run(map[bool]string{true: "allowed", false: "refused"}[allowed], func(t *testing.T) {
			root := t.TempDir()
			t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "config"))
			t.Setenv("XDG_DATA_HOME", filepath.Join(root, "data"))

			request := asked.Request{
				From: node.ID{}, Name: "bob", Path: "/notes", At: time.Now(),
			}
			if err := asked.Ring(request); err != nil {
				t.Fatal(err)
			}
			if err := answering("notes/", "bob", allowed); err != nil {
				t.Fatal(err)
			}

			pending, err := asked.All()
			if err != nil {
				t.Fatal(err)
			}
			if len(pending) != 0 {
				t.Fatalf("the answered request remains: %+v", pending)
			}

			store, err := grant.Load()
			if err != nil {
				t.Fatal(err)
			}
			allow, deny := store.For("/notes")
			if allowed && !has(allow, "bob") {
				t.Fatal("the canonical path does not allow bob")
			}
			if !allowed && !has(deny, "bob") {
				t.Fatal("the canonical path does not refuse bob")
			}
		})
	}
}

func TestWaitingNormalizesTheRequestedPath(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_DATA_HOME", filepath.Join(root, "data"))

	if err := asked.Ring(asked.Request{
		From: node.ID{}, Name: "bob", Path: "/notes", At: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	wanting, err := waiting("//notes/")
	if err != nil {
		t.Fatal(err)
	}
	if len(wanting) != 1 || wanting[0].Who != "bob" {
		t.Fatalf("waiting() = %+v", wanting)
	}
}
