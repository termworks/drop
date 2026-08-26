package conf

import (
	"fmt"

	"github.com/bresilla/drop/src/pkg/made"
	"github.com/bresilla/drop/src/pkg/ns"
)

// Created mounts the namespaces a command wrote down, beside the ones the config declares.
//
// It hands back what it left out, because a path that was created and is not being served is worth
// a line rather than a silence.
//
// A collision is settled by the config, always, and settled explicitly rather than by the order
// these are added in: the table replaces whatever was at a path, so a merge that leant on ordering
// would swap the winner the next time somebody moved a call.
//
// An archetype this build does not answer to is skipped and is not fatal. A config that names one
// is refused where it is written, because a config is structure and a typo in it should stop the
// node; this is data, and data follows the grants, which fail closed on the entry they cannot read
// and go on with the rest.
func (c *Config) Created(store *made.Store) ([]made.Skipped, error) {
	if store == nil {
		return nil, nil
	}
	if c.known == nil {
		return nil, fmt.Errorf("this build registered no namespace types")
	}

	declared := map[string]ns.Mount{}
	for _, m := range c.Mounts.All() {
		declared[m.Path] = m
	}

	var left []made.Skipped
	for _, at := range store.Paths() {
		entry, ok := store.Get(at)
		if !ok {
			continue
		}

		skip := func(why string) {
			left = append(left, made.Skipped{Path: at, Archetype: entry.Archetype, Why: why})
		}

		if m, ok := declared[at]; ok && m.Source == ns.Configured {
			skip("the config declares it, so the config is what is served")
			continue
		}

		answers, ok := c.known.Lookup(entry.Archetype, entry.Version)
		if !ok {
			skip("this build does not answer to that, so nothing is served there")
			continue
		}
		settings, err := answers.Read(made.Declared(entry.Settings))
		if err != nil {
			skip(err.Error())
			continue
		}

		m := ns.Mount{
			Path:      at,
			Source:    ns.Written,
			Archetype: entry.Archetype,
			Version:   entry.Version,
			Config:    settings,
			Access:    entry.Access.Rule(),
			Shared:    entry.Shared,
		}
		if err := c.Mounts.Add(m); err != nil {
			skip(err.Error())
		}
	}
	return left, nil
}
