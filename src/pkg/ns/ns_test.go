package ns

import "testing"

func TestCleanNormalises(t *testing.T) {
	cases := map[string]string{
		"stream/1":     "/stream/1",
		"/stream/1":    "/stream/1",
		"/stream/1/":   "/stream/1",
		"//stream//1":  "/stream/1",
		"":             Root,
		"/":            Root,
		"/a/b/c/d/e":   "/a/b/c/d/e",
		"in-box_2.old": "/in-box_2.old",
	}

	for given, want := range cases {
		got, err := Clean(given)
		if err != nil {
			t.Errorf("Clean(%q): %v", given, err)
			continue
		}
		if got != want {
			t.Errorf("Clean(%q) = %q, want %q", given, got, want)
		}
	}
}

// A path is compared, logged and used to pick a directory, so what may appear in one is narrow.
func TestCleanRefusesTrouble(t *testing.T) {
	for _, bad := range []string{"/../etc", "/a/../b", "/UPPER", "/has space", "/semi;colon", "/sla\\sh"} {
		if got, err := Clean(bad); err == nil {
			t.Errorf("Clean(%q) = %q, want an error", bad, got)
		}
	}
}

func TestCleanBoundsDepthAndLength(t *testing.T) {
	deep := ""
	for i := 0; i <= MaxDepth; i++ {
		deep += "/a"
	}
	if _, err := Clean(deep); err == nil {
		t.Error("Clean() accepted a path past the depth limit")
	}

	long := "/"
	for len(long) <= MaxLength {
		long += "a"
	}
	if _, err := Clean(long); err == nil {
		t.Error("Clean() accepted a path past the length limit")
	}
}

func TestParseAddress(t *testing.T) {
	cases := []struct {
		text    string
		user    string
		machine string
		path    string
		here    bool
	}{
		{"laptop", "", "laptop", Root, false},
		{"laptop:/inbox", "", "laptop", "/inbox", false},
		{"bob:laptop:/chat", "bob", "laptop", "/chat", false},
		{"bob::/chat", "bob", "", "/chat", false},
		{"laptop:/stream/of/one/specific/namespace", "", "laptop", "/stream/of/one/specific/namespace", false},
		{"12D3KooWCK6Vkp:/chat", "", "12D3KooWCK6Vkp", "/chat", false},
		{"/chat", "", "", "/chat", true},
		{":/chat", "", "", "/chat", true},
	}

	for _, c := range cases {
		got, err := ParseAddress(c.text)
		if err != nil {
			t.Errorf("ParseAddress(%q): %v", c.text, err)
			continue
		}
		if got.User != c.user || got.Machine != c.machine || got.Path != c.path || got.Here != c.here {
			t.Errorf("ParseAddress(%q) = %+v, want %s %s %s here=%v", c.text, got, c.user, c.machine, c.path, c.here)
		}
	}

	// A slash where a name goes is the old form, and it is not this one.
	for _, bad := range []string{"", "   ", "laptop/inbox", "bob:laptop:chat", "::"} {
		if _, err := ParseAddress(bad); err == nil {
			t.Errorf("ParseAddress(%q) succeeded, want an error", bad)
		}
	}
}

// The longest declared prefix wins, so a general namespace serves everything below it while a more
// specific one still takes precedence.
func TestLookupPrefersTheLongestPrefix(t *testing.T) {
	table := NewTable()
	mustAdd(t, table, Mount{Path: "/stream", Archetype: "stream", Config: "general"})
	mustAdd(t, table, Mount{Path: "/stream/logs", Archetype: "stream", Config: "specific"})

	cases := []struct {
		ask    string
		served string
		rest   string
	}{
		{"/stream", "general", "/"},
		{"/stream/of/one/specific/namespace", "general", "/of/one/specific/namespace"},
		{"/stream/logs", "specific", "/"},
		{"/stream/logs/today", "specific", "/today"},
	}

	for _, c := range cases {
		m, rest, ok := table.Lookup(c.ask)
		if !ok {
			t.Errorf("Lookup(%q) found nothing", c.ask)
			continue
		}
		if m.Config != c.served || rest != c.rest {
			t.Errorf("Lookup(%q) = %v rest %q, want %s rest %q", c.ask, m.Config, rest, c.served, c.rest)
		}
	}
}

// Without a segment-boundary check, /stream would capture /streaming.
func TestLookupDoesNotCaptureASimilarName(t *testing.T) {
	table := NewTable()
	mustAdd(t, table, Mount{Path: "/stream", Archetype: "stream", Config: "x"})

	if _, _, ok := table.Lookup("/streaming"); ok {
		t.Fatal("/stream captured /streaming")
	}
}

// Re-declaring a path replaces it, so a config that is re-read cannot grow the table.
func TestAddIsKeyedByPath(t *testing.T) {
	table := NewTable()
	mustAdd(t, table, Mount{Path: "/inbox", Archetype: "share", Config: "/first"})
	mustAdd(t, table, Mount{Path: "/inbox", Archetype: "share", Config: "/second"})

	if table.Len() != 1 {
		t.Fatalf("Len() = %d, want 1", table.Len())
	}
	m, _, _ := table.Lookup("/inbox")
	if m.Config != "/second" {
		t.Fatalf("Config = %v, want the later declaration", m.Config)
	}
}

func TestAddNormalisesThePath(t *testing.T) {
	table := NewTable()
	mustAdd(t, table, Mount{Path: "inbox/", Archetype: "share", Config: "/x"})

	if _, _, ok := table.Lookup("/inbox"); !ok {
		t.Fatal("a mount declared as inbox/ is not found at /inbox")
	}
}

func TestAddRefusesAnUntypedMount(t *testing.T) {
	table := NewTable()
	if err := table.Add(Mount{Path: "/x"}); err == nil {
		t.Fatal("Add() accepted a mount with no type")
	}
}

func TestRootServesEverything(t *testing.T) {
	table := NewTable()
	mustAdd(t, table, Mount{Path: "/", Archetype: "share", Config: "/x"})

	m, rest, ok := table.Lookup("/anything/at/all")
	if !ok || m.Config != "/x" || rest != "/anything/at/all" {
		t.Fatalf("Lookup() = %+v rest %q ok %v", m, rest, ok)
	}
}

// A namespace knows which archetype it belongs to and nothing about what that means: whatever the
// archetype made of the declaration is carried back untouched.
func TestAMountCarriesItsConfigUnread(t *testing.T) {
	type settings struct {
		Dir      string
		Writable bool
	}

	table := NewTable()
	mustAdd(t, table, Mount{Path: "/work", Archetype: "files", Config: settings{Dir: "/x", Writable: true}})

	m, _, ok := table.Lookup("/work")
	if !ok {
		t.Fatal("Lookup() found nothing")
	}
	if got, ok := m.Config.(settings); !ok || got.Dir != "/x" || !got.Writable {
		t.Errorf("the config came back as %#v", m.Config)
	}
}

// A mount with no archetype is a branch: it serves nothing and carries a rule for what is under it.
func TestAMountWithNoTypeIsABranch(t *testing.T) {
	table := NewTable()
	mustAdd(t, table, Mount{Path: "/friends", Access: Access{AnyPaired: true}})

	m, _, ok := table.Lookup("/friends")
	if !ok || !m.Branch() {
		t.Errorf("Lookup() = %+v, want a branch", m)
	}
}

func mustAdd(t *testing.T, table *Table, m Mount) {
	t.Helper()
	if err := table.Add(m); err != nil {
		t.Fatalf("Add(%q): %v", m.Path, err)
	}
}

// What an address prints as has to read back as the same address: it is what an error message
// quotes back at somebody, and a form they cannot retype is worse than no form at all.
func TestAnAddressReadsBackAsItself(t *testing.T) {
	for _, text := range []string{
		"bob:laptop:/chat", "laptop:/chat", "bob::/chat", "/chat",
		"bob:laptop", "laptop", "orin:/work/deep",
	} {
		at, err := ParseAddress(text)
		if err != nil {
			t.Errorf("ParseAddress(%q): %v", text, err)
			continue
		}
		if got := at.String(); got != text {
			t.Errorf("ParseAddress(%q).String() = %q", text, got)
		}
		again, err := ParseAddress(at.String())
		if err != nil || again != at {
			t.Errorf("%q printed as %q, which reads back as %+v (%v)", text, at.String(), again, err)
		}
	}
}
