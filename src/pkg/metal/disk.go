package metal

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// The drive the system is on, and its serial.
//
// Which drive matters. A machine with six of them must pick the same one every boot, and the names
// the kernel gives them are not that: nvme0n1 and nvme1n1 change places depending on which
// controller answered first. So the drive is found by asking which one the root filesystem is
// actually on, and following it down through whatever is stacked on top of it — a partition, an
// encrypted volume, a logical volume — until real hardware with a serial written on it is reached.

// deep is how many layers of one thing built on another are followed before giving up. Encryption
// on a volume group on a partition is three; anything much past that is a loop.
const deep = 8

// fromDisk is the machine as the drive it boots off.
func fromDisk() (Mark, error) {
	at, err := os.Stat("/")
	if err != nil {
		return Mark{}, fmt.Errorf("looking at the root filesystem: %w", err)
	}
	held, ok := at.Sys().(*syscall.Stat_t)
	if !ok {
		return Mark{}, fmt.Errorf("this system does not say what device the root filesystem is on")
	}

	dev := fmt.Sprintf("%d:%d", major(held.Dev), minor(held.Dev))
	found := beneath(filepath.Join("/sys/dev/block", dev), deep)
	if len(found) == 0 {
		return Mark{}, fmt.Errorf("nothing under the root filesystem has a serial written on it")
	}

	says := "the serial on the drive the system is on"
	if len(found) > 1 {
		says = fmt.Sprintf("the serials on the %d drives the system is spread over", len(found))
	}
	return Mark{From: Disk, Says: says, raw: gathered(found)}, nil
}

// beneath is the serials of the real drives underneath a block device.
//
// A device that is built on others says so in slaves/, and one that is a partition of a drive says
// so by having the drive as its parent. Either way the walk ends at something with a serial, which
// is the only thing here that is written on hardware rather than made up by the kernel.
func beneath(at string, left int) []string {
	if left <= 0 {
		return nil
	}

	if serial, ok := written(at); ok {
		return []string{serial}
	}

	// Something stacked on other devices: every one of them, so a mirror is named by both halves
	// and does not change its mind when one is missing at boot.
	var out []string
	slaves, err := os.ReadDir(filepath.Join(at, "slaves"))
	if err == nil {
		for _, slave := range slaves {
			out = append(out, beneath(filepath.Join(at, "slaves", slave.Name()), left-1)...)
		}
	}
	if len(out) > 0 {
		return out
	}

	// A partition: the drive it is cut from is the directory above it.
	up, err := filepath.EvalSymlinks(at)
	if err != nil {
		return nil
	}
	parent := filepath.Dir(up)
	if _, err := os.Stat(filepath.Join(parent, "dev")); err != nil {
		return nil
	}
	return beneath(parent, left-1)
}

// written is the serial a drive has on it, if it is a drive and it has one.
func written(at string) (string, bool) {
	for _, name := range []string{"device/serial", "device/wwid", "wwid", "serial"} {
		raw, err := os.ReadFile(filepath.Join(at, name))
		if err != nil {
			continue
		}
		material, ok := steady(raw)
		if !ok {
			continue
		}
		// The kind of drive as well as its serial: two drives from different makers are allowed
		// to have printed the same number on themselves.
		kind := ""
		if raw, err := os.ReadFile(filepath.Join(at, "device/model")); err == nil {
			kind = strings.TrimSpace(string(raw))
		}
		return kind + "/" + string(material), true
	}
	return "", false
}

func major(dev uint64) uint64 { return (dev >> 8) & 0xfff }
func minor(dev uint64) uint64 { return dev&0xff | ((dev >> 12) & 0xfff00) }
