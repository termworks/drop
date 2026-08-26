package share

import "testing"

// no is a declaration that says nothing at all.
type no struct{}

func (no) String(string) (string, bool)    { return "", false }
func (no) Bool(string) (bool, bool)        { return false, false }
func (no) Strings(string) ([]string, bool) { return nil, false }

// A share with nowhere to put anything cannot work, and saying so at load time is the difference
// between a config error and silence months later.
func TestAShareNeedsADir(t *testing.T) {
	if _, err := (&Share{}).Read(no{}); err == nil {
		t.Fatal("Read() accepted a share with no dir")
	}
}

func TestAShareReadsItsDir(t *testing.T) {
	got, err := (&Share{}).Read(one{"dir": "/tmp/in"})
	if err != nil {
		t.Fatalf("Read(): %v", err)
	}
	if got != (Config{Dir: "/tmp/in"}) {
		t.Fatalf("Read() = %+v", got)
	}
}

// one is a declaration of a handful of settings.
type one map[string]string

func (o one) String(key string) (string, bool) { v, ok := o[key]; return v, ok }
func (o one) Bool(string) (bool, bool)         { return false, false }
func (o one) Strings(string) ([]string, bool)  { return nil, false }
