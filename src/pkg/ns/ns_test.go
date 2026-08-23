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
		text string
		peer string
		path string
	}{
		{"laptop", "laptop", Root},
		{"laptop/inbox", "laptop", "/inbox"},
		{"laptop/stream/of/one/specific/namespace", "laptop", "/stream/of/one/specific/namespace"},
		{"12D3KooWCK6Vkp/chat", "12D3KooWCK6Vkp", "/chat"},
	}

	for _, c := range cases {
		got, err := ParseAddress(c.text)
		if err != nil {
			t.Errorf("ParseAddress(%q): %v", c.text, err)
			continue
		}
		if got.Peer != c.peer || got.Path != c.path {
			t.Errorf("ParseAddress(%q) = %+v, want %s %s", c.text, got, c.peer, c.path)
		}
	}

	for _, bad := range []string{"", "   ", "/inbox"} {
		if _, err := ParseAddress(bad); err == nil {
			t.Errorf("ParseAddress(%q) succeeded, want an error", bad)
		}
	}
}

// The longest declared prefix wins, so a general namespace serves everything below it while a more
// specific one still takes precedence.
func TestLookupPrefersTheLongestPrefix(t *testing.T) {
	table := NewTable()
	mustAdd(t, table, Mount{Path: "/stream", Kind: KindStream, Command: "general"})
	mustAdd(t, table, Mount{Path: "/stream/logs", Kind: KindStream, Command: "specific"})

	cases := []struct {
		ask     string
		command string
		rest    string
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
		if m.Command != c.command || rest != c.rest {
			t.Errorf("Lookup(%q) = %s rest %q, want %s rest %q", c.ask, m.Command, rest, c.command, c.rest)
		}
	}
}

// Without a segment-boundary check, /stream would capture /streaming.
func TestLookupDoesNotCaptureASimilarName(t *testing.T) {
	table := NewTable()
	mustAdd(t, table, Mount{Path: "/stream", Kind: KindStream, Command: "x"})

	if _, _, ok := table.Lookup("/streaming"); ok {
		t.Fatal("/stream captured /streaming")
	}
}

// Re-declaring a path replaces it, so a config that is re-read cannot grow the table.
func TestAddIsKeyedByPath(t *testing.T) {
	table := NewTable()
	mustAdd(t, table, Mount{Path: "/inbox", Kind: KindFiles, Dir: "/first"})
	mustAdd(t, table, Mount{Path: "/inbox", Kind: KindFiles, Dir: "/second"})

	if table.Len() != 1 {
		t.Fatalf("Len() = %d, want 1", table.Len())
	}
	m, _, _ := table.Lookup("/inbox")
	if m.Dir != "/second" {
		t.Fatalf("Dir = %q, want the later declaration", m.Dir)
	}
}

func TestAddNormalisesThePath(t *testing.T) {
	table := NewTable()
	mustAdd(t, table, Mount{Path: "inbox/", Kind: KindFiles, Dir: "/x"})

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
	mustAdd(t, table, Mount{Path: "/", Kind: KindFiles, Dir: "/x"})

	m, rest, ok := table.Lookup("/anything/at/all")
	if !ok || m.Dir != "/x" || rest != "/anything/at/all" {
		t.Fatalf("Lookup() = %+v rest %q ok %v", m, rest, ok)
	}
}

func TestParseKind(t *testing.T) {
	for _, name := range []string{"files", "stream", "tty", "chat", "link"} {
		kind, err := ParseKind(name)
		if err != nil {
			t.Errorf("ParseKind(%q): %v", name, err)
			continue
		}
		if kind.String() != name {
			t.Errorf("ParseKind(%q).String() = %q", name, kind.String())
		}
	}
	if _, err := ParseKind("nonsense"); err == nil {
		t.Error("ParseKind() accepted a type that does not exist")
	}
}

func mustAdd(t *testing.T, table *Table, m Mount) {
	t.Helper()
	if err := table.Add(m); err != nil {
		t.Fatalf("Add(%q): %v", m.Path, err)
	}
}
