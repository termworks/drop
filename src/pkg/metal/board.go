package metal

import (
	"fmt"
	"os"
)

// What the firmware will say about the machine, without being asked as root.
//
// Most of these are readable by anybody, and the two best ones are not. They are tried anyway: a
// drop running as a system service, or on a machine where somebody has widened the permissions on
// purpose, gets the better name for free, and one that cannot read them is no worse off than if
// they had never been tried.

// boardAt is where firmware serials live, best first. On a machine with a device tree there is no
// DMI at all and the serial is its own file; on a PC it is the other way round.
var boardAt = []string{
	"/sys/class/dmi/id/product_uuid",
	"/sys/class/dmi/id/board_serial",
	"/sys/class/dmi/id/product_serial",
	"/sys/firmware/devicetree/base/serial-number",
	"/proc/device-tree/serial-number",
}

// fromBoard is the machine as its firmware knows it.
func fromBoard() (Mark, error) {
	for _, at := range boardAt {
		raw, err := os.ReadFile(at)
		if err != nil {
			continue
		}
		material, ok := steady(raw)
		if !ok {
			continue
		}
		return Mark{From: Board, Says: fmt.Sprintf("the serial the firmware holds, from %s", at), raw: material}, nil
	}
	return Mark{}, fmt.Errorf("the firmware here says nothing this machine could be named by")
}
