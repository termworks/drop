package tui

import (
	"sort"
	"strings"

	"github.com/bresilla/drop/src/pkg/proto"
)

// A device's paths are a tree, and it is walked like one.
//
// A config declares whole paths — /work/reports, /work/notes, /media/music — and a flat list of
// them is unreadable the moment there are more than a handful. What a person wants is what they
// want of a filesystem: see what is at this level, go into one, come back out.
//
// Nothing is declared as a folder for this to work. A folder is what it means for two paths to
// share a beginning, so the tree is read out of the paths themselves.

// step is one line of a path listing: a namespace, or the way down to more of them.
type step struct {
	// name is the segment, without the path leading up to it.
	name string
	// at is the whole path this step stands for.
	at string
	// served is the namespace here, when the path itself is one.
	served proto.Served
	// is says whether there is a namespace at this path.
	is bool
	// below is how many namespaces are underneath, when it is a way down.
	below int
}

// walk lists what is directly under a path: the namespaces at this level, and the ways down.
func walk(paths []proto.Served, under string) []step {
	under = folder(under)

	seen := map[string]*step{}
	for _, s := range paths {
		if !within(under, s.Path) {
			continue
		}

		rest := strings.TrimPrefix(strings.TrimPrefix(s.Path, strings.TrimSuffix(under, "/")), "/")
		if rest == "" {
			continue // the path we are already looking at
		}

		name, deeper, isFolder := strings.Cut(rest, "/")
		at := folder(under) + name

		here, ok := seen[name]
		if !ok {
			here = &step{name: name, at: at}
			seen[name] = here
		}

		if isFolder && deeper != "" {
			// Something lives below this segment, so it is a way down as well as possibly a
			// namespace in its own right.
			here.below++
			continue
		}

		here.served, here.is = s, true
	}

	out := make([]step, 0, len(seen))
	for _, one := range seen {
		out = append(out, *one)
	}

	// Ways down first, then namespaces, each in path order: the same arrangement every file
	// browser has, because it is the one people already know.
	sort.Slice(out, func(i, j int) bool {
		if (out[i].below > 0) != (out[j].below > 0) {
			return out[i].below > 0
		}
		return out[i].name < out[j].name
	})
	return out
}

// within reports whether a path is at or below a folder.
func within(under, path string) bool {
	if under == "/" {
		return true
	}
	under = strings.TrimSuffix(under, "/")
	return path == under || strings.HasPrefix(path, under+"/")
}

// folder is a path in the form other paths hang off.
func folder(at string) string {
	if at == "" || at == "/" {
		return "/"
	}
	return strings.TrimSuffix(at, "/") + "/"
}

// up is the path one level above this one.
func up(at string) string {
	at = strings.TrimSuffix(at, "/")
	if at == "" || at == "/" {
		return "/"
	}

	cut := strings.LastIndex(at, "/")
	if cut <= 0 {
		return "/"
	}
	return at[:cut]
}
