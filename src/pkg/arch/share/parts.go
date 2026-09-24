package share

import (
	"errors"
	"io"
	"io/fs"
	"math"
	"os"
	"sort"
	"strings"
)

const (
	partPrefix          = ".drop-"
	partSuffix          = ".part"
	partDigestBytes     = 16
	maxRetainedParts    = 1024
	maxLandingNameBytes = 240
)

type retainedPart struct {
	name     string
	size     int64
	modified int64
}

func receiverPart(name string) bool {
	if len(name) != len(partPrefix)+partDigestBytes*2+len(partSuffix) ||
		!strings.HasPrefix(name, partPrefix) || !strings.HasSuffix(name, partSuffix) {
		return false
	}
	digest := name[len(partPrefix) : len(name)-len(partSuffix)]
	for _, c := range []byte(digest) {
		digit := c >= '0' && c <= '9'
		hex := c >= 'a' && c <= 'f'
		if !digit && !hex {
			return false
		}
	}
	return true
}

func trimParts(dir *os.Root, limit int64) (outErr error) {
	opened, err := dir.Open(".")
	if err != nil {
		return err
	}
	defer func() { outErr = errors.Join(outErr, opened.Close()) }()

	parts := make([]retainedPart, 0)
	total := int64(0)
	for {
		entries, readErr := opened.ReadDir(256)
		for _, entry := range entries {
			if !receiverPart(entry.Name()) {
				continue
			}
			stat, err := dir.Lstat(entry.Name())
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			if err != nil {
				return err
			}
			if !stat.Mode().IsRegular() {
				continue
			}
			parts = append(parts, retainedPart{name: entry.Name(), size: stat.Size(), modified: stat.ModTime().UnixNano()})
			if stat.Size() > math.MaxInt64-total {
				total = math.MaxInt64
			} else {
				total += stat.Size()
			}
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return readErr
		}
	}
	left := len(parts)
	if total <= limit && left <= maxRetainedParts {
		return nil
	}
	sort.Slice(parts, func(i, j int) bool {
		if parts[i].modified == parts[j].modified {
			return parts[i].name < parts[j].name
		}
		return parts[i].modified < parts[j].modified
	})
	removed := false
	for _, part := range parts {
		if total <= limit && left <= maxRetainedParts {
			break
		}
		if err := dir.Remove(part.name); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		removed = true
		total -= part.size
		left--
	}
	if removed {
		return syncDir(dir)
	}
	return nil
}
