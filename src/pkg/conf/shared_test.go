package conf

import (
	"strings"
	"testing"
)

// asSomebody gives this machine a config directory of its own, so the key a shared namespace is
// named after is one this test made.
func asSomebody(t *testing.T) {
	t.Helper()

	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("DROP_USER_KEY", "")
}

func mountAt(t *testing.T, cfg *Config, at string) (shared bool, id string) {
	t.Helper()

	m, _, ok := cfg.Mounts.Lookup(at)
	if !ok || m.Path != at {
		t.Fatalf("nothing is mounted at %s", at)
	}
	return m.Shared.Declared(), m.Shared.ID()
}

// A config says a namespace is one several machines hold, and it is named after the person whose
// config it is.
func TestAConfigDeclaresANamespaceSeveralMachinesHold(t *testing.T) {
	asSomebody(t)

	cfg := load(t, `
		local drop = require("drop")
		drop.mount("/notes", { type = "chat", access = "paired", shared = true })
		drop.mount("/chat",  { type = "chat", access = "paired" })
	`)

	shared, id := mountAt(t, cfg, "/notes")
	if !shared {
		t.Fatal("/notes was declared shared and is not")
	}
	if len(id) != 64 {
		t.Fatalf("/notes is called %q", id)
	}

	if shared, _ := mountAt(t, cfg, "/chat"); shared {
		t.Fatal("/chat said nothing and is shared anyway")
	}
}

// The same file read twice names the same thing, which is the whole reason the name is worked out
// rather than minted: a config is read again at every start.
func TestReadingTheSameConfigTwiceNamesTheSameThing(t *testing.T) {
	asSomebody(t)

	body := `
		local drop = require("drop")
		drop.mount("/notes", { type = "chat", access = "paired", shared = true })
	`

	write(t, body)
	first, err := Load(known())
	if err != nil {
		t.Fatalf("Load(): %v", err)
	}
	defer first.Close()

	again, err := Load(known())
	if err != nil {
		t.Fatalf("Load() again: %v", err)
	}
	defer again.Close()

	_, was := mountAt(t, first, "/notes")
	_, now := mountAt(t, again, "/notes")
	if was != now {
		t.Fatalf("it was called %s and is now called %s", was, now)
	}
}

// Writing a different word is how somebody says this is a new thing at an old path.
func TestADifferentWordIsADifferentThing(t *testing.T) {
	asSomebody(t)

	first := load(t, `
		local drop = require("drop")
		drop.mount("/notes", { type = "chat", access = "paired", shared = true })
	`)
	_, was := mountAt(t, first, "/notes")

	again := load(t, `
		local drop = require("drop")
		drop.mount("/notes", { type = "chat", access = "paired", shared = "second" })
	`)
	_, now := mountAt(t, again, "/notes")

	if was == now {
		t.Fatalf("both are called %s", was)
	}
}

// A terminal is somebody's screen. There is no sense in which two of them are the same one, and
// saying so where it is written is better than a namespace nobody can join for reasons.
func TestAnArchetypeThatIsOneMachinesOwnCannotBeShared(t *testing.T) {
	asSomebody(t)

	write(t, `
		local drop = require("drop")
		drop.mount("/term", { type = "tty", access = "paired", shared = true })
	`)

	if _, err := Load(known()); err == nil {
		t.Fatal("a shared terminal was declared")
	} else if !strings.Contains(err.Error(), "one machine's own") {
		t.Fatalf("Load() = %v", err)
	}
}

// A path that holds others and serves nothing has nothing to share.
func TestABranchCannotBeShared(t *testing.T) {
	asSomebody(t)

	write(t, `
		local drop = require("drop")
		drop.mount("/work", { access = "paired", shared = true })
	`)

	if _, err := Load(known()); err == nil {
		t.Fatal("a branch was declared shared")
	} else if !strings.Contains(err.Error(), "nothing to share") {
		t.Fatalf("Load() = %v", err)
	}
}
