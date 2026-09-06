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
	if most < 0 || most == math.MaxInt64 {
		return nil, fmt.Errorf("reading %s: invalid size limit %d", file, most)
	}

	opened, err := os.OpenFile(file, os.O_RDONLY|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	defer func() { _ = opened.Close() }()

	stat, err := opened.Stat()
	if err != nil {
		return nil, fmt.Errorf("stating %s: %w", file, err)
	}
	if !stat.Mode().IsRegular() {
		return nil, fmt.Errorf("reading %s: it is not a regular file", file)
	}
	if stat.Size() > most {
		return nil, fmt.Errorf("reading %s: %d bytes, over the %d-byte limit", file, stat.Size(), most)
	}

	raw, err := io.ReadAll(io.LimitReader(opened, most+1))
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", file, err)
	}
	if int64(len(raw)) > most {
		return nil, fmt.Errorf("reading %s: more than the %d-byte limit", file, most)
	}
	return raw, nil
}
