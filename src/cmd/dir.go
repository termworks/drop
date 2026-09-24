package cmd

import (
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
)

func readDirUpTo(dir string, most int) ([]os.DirEntry, error) {
	opened, err := os.Open(dir)
	if err != nil {
		return nil, err
	}
	defer func() { _ = opened.Close() }()

	entries, err := opened.ReadDir(most + 1)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	if len(entries) > most {
		return nil, fmt.Errorf("directory has more than %d entries", most)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	return entries, nil
}
