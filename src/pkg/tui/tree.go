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

// step is one line of a path listing: a namespace, the way down to more of them, or both.
type step struct {
	// here marks the row standing for the path being looked at, which appears inside a path that
	// is a namespace as well as a way down.
	here bool

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

	var out []step

	seen := map[string]*step{}
	for _, s := range paths {
		if !within(under, s.Path) {
			continue
		}

		rest := strings.TrimPrefix(strings.TrimPrefix(s.Path, strings.TrimSuffix(under, "/")), "/")

		// The path being looked at is itself a namespace. A path can be both a place and a way
		// down — /one/two serving something, with /one/two/three under it — and walking in has to
		// leave a way to open the thing walked into. So it is listed inside itself, the way a
		// directory listing has an entry for the directory.
		if rest == "" {
			out = append(out, step{
				here:   true,
				name:   lastPart(s.Path),
				at:     s.Path,
				served: s,
				is:     true,
			})
			continue
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

	rest := make([]step, 0, len(seen))
	for _, one := range seen {
		rest = append(rest, *one)
	}

	// Ways down first, then namespaces, each in path order: the same arrangement every file
	// browser has, because it is the one people already know.
	sort.Slice(rest, func(i, j int) bool {
		if (rest[i].below > 0) != (rest[j].below > 0) {
			return rest[i].below > 0
		}
		return rest[i].name < rest[j].name
	})

	// The path itself first, because it is what was entered.
	return append(out, rest...)
}

// lastPart is the final segment of a path.
func lastPart(at string) string {
	at = strings.TrimSuffix(at, "/")
	if cut := strings.LastIndex(at, "/"); cut >= 0 {
		return at[cut+1:]
	}
	return at
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
