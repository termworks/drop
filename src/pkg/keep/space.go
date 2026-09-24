package keep

import (
	"fmt"
	"math"
	"os"

	"golang.org/x/sys/unix"
)

// FreeReserve is the disk space incoming files leave unused.
const FreeReserve int64 = 64 << 20

// Room checks whether file's filesystem can take want bytes and keep the reserve.
func Room(file *os.File, want int64) error {
	if want < 0 {
		return fmt.Errorf("invalid incoming size %d", want)
	}

	var stat unix.Statfs_t
	if err := unix.Fstatfs(int(file.Fd()), &stat); err != nil {
		return fmt.Errorf("checking free space: %w", err)
	}

	available := uint64(0)
	if stat.Bavail > 0 && stat.Bsize > 0 {
		blocks, size := uint64(stat.Bavail), uint64(stat.Bsize)
		available = uint64(math.MaxInt64)
		if blocks <= uint64(math.MaxInt64)/size {
			available = blocks * size
		}
	}
	needed := uint64(want) + uint64(FreeReserve)
	if needed < uint64(want) || needed > available {
		return fmt.Errorf("%d bytes need to land while %d bytes stay free", want, FreeReserve)
	}
	return nil
}

// RoomIn checks free space in an open rooted directory.
func RoomIn(dir *os.Root, want int64) error {
	opened, err := dir.Open(".")
	if err != nil {
		return fmt.Errorf("opening the receiving directory: %w", err)
	}
	defer func() { _ = opened.Close() }()
	return Room(opened, want)
}
