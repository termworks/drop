package keep

import (
	"fmt"
	"io"
	"math"
	"os"

	"golang.org/x/sys/unix"
)

// MaxState is the largest small state file drop will hold in memory.
const MaxState int64 = 16 << 20

// ReadFile reads one bounded regular file without waiting on a special file under its name.
func ReadFile(file string, most int64) ([]byte, error) {
	raw, _, err := ReadFileInfo(file, most)
	return raw, err
}

// ReadFileInfo reads one bounded regular file and identifies the revision that was read.
func ReadFileInfo(file string, most int64) ([]byte, os.FileInfo, error) {
	if most < 0 || most == math.MaxInt64 {
		return nil, nil, fmt.Errorf("reading %s: invalid size limit %d", file, most)
	}

	opened, err := os.OpenFile(file, os.O_RDONLY|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = opened.Close() }()

	stat, err := opened.Stat()
	if err != nil {
		return nil, nil, fmt.Errorf("stating %s: %w", file, err)
	}
	if !stat.Mode().IsRegular() {
		return nil, nil, fmt.Errorf("reading %s: it is not a regular file", file)
	}
	if stat.Size() > most {
		return nil, nil, fmt.Errorf("reading %s: %d bytes, over the %d-byte limit", file, stat.Size(), most)
	}

	raw, err := io.ReadAll(io.LimitReader(opened, most+1))
	if err != nil {
		return nil, nil, fmt.Errorf("reading %s: %w", file, err)
	}
	if int64(len(raw)) > most {
		return nil, nil, fmt.Errorf("reading %s: more than the %d-byte limit", file, most)
	}
	after, err := opened.Stat()
	if err != nil {
		return nil, nil, fmt.Errorf("stating %s after reading it: %w", file, err)
	}
	if stat.Size() != after.Size() || !stat.ModTime().Equal(after.ModTime()) {
		return nil, nil, fmt.Errorf("reading %s: it changed while it was read", file)
	}
	return raw, after, nil
}
