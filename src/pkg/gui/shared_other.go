//go:build android

package gui

// Shared is something handed over from outside.
//
// On a phone this arrives as an intent rather than through the bridge, so nothing here reads it yet.
// The type exists on both so the screens can be written once.
type Shared struct {
	Title string
	Text  string
	URL   string
	Name  string
	Size  int64
}

func (s *Shared) What() string {
	if s.Name != "" {
		return s.Name
	}
	if s.URL != "" {
		return s.URL
	}
	return s.Text
}

func (s *Shared) Body() string {
	if s.URL != "" {
		return s.URL
	}
	return s.Text
}

func (s *Shared) IsLink() bool { return s.URL != "" }

// Claim has nothing to take: a phone is handed its share directly.
func (r *Remote) Claim() (*Shared, error) { return nil, nil }
