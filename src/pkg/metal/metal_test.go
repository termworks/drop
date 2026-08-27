package metal

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// What a machine is named by has to be worth naming it by, and the same on every boot. Most of
// what firmware reports is neither.

func TestFirmwareBoilerplateIsNotAName(t *testing.T) {
	for _, junk := range []string{
		"To be filled by O.E.M.", "Default string", "None", "unknown", "N/A",
		"", "  ", "abc", "00000000", "ffffffff", "\x00\x00\x00\x00\x00",
		"System Serial Number", "not specified",
	} {
		if _, ok := steady([]byte(junk)); ok {
			t.Errorf("%q was taken for a machine's name", junk)
		}
	}

	for _, real := range []string{"S4EWNX0R540114Y", "1421823004485", "  padded-serial  "} {
		got, ok := steady([]byte(real))
		if !ok {
			t.Errorf("%q was refused as a machine's name", real)
			continue
		}
		if strings.TrimSpace(string(got)) != string(got) {
			t.Errorf("%q came back with space still on it: %q", real, got)
		}
	}
}

// Two drives read in either order are one machine, not two.
func TestTheOrderThingsAreReadInDoesNotChangeTheMachine(t *testing.T) {
	one := gathered([]string{"a/111", "b/222", "c/333"})
	two := gathered([]string{"c/333", "a/111", "b/222"})
	if string(one) != string(two) {
		t.Fatalf("the same drives in another order made another machine: %q against %q", one, two)
	}
	if string(gathered([]string{"a/111"})) == string(one) {
		t.Fatal("one drive and three drives came out the same")
	}
}

// A name derived for drop must not be the name anything else would derive from the same serial,
// and a serial off a drive must not collide with the same digits off a board.
func TestOneSerialGivesDifferentNamesForDifferentThings(t *testing.T) {
	raw := []byte("1421823004485")

	disk, err := Mark{From: Disk, raw: raw}.Seed()
	if err != nil {
		t.Fatal(err)
	}
	board, err := Mark{From: Board, raw: raw}.Seed()
	if err != nil {
		t.Fatal(err)
	}
	if disk == board {
		t.Fatal("the same digits off a drive and off a board named the same machine")
	}

	// And the same thing twice is the same machine, which is the whole point.
	again, err := Mark{From: Disk, raw: raw}.Seed()
	if err != nil {
		t.Fatal(err)
	}
	if disk != again {
		t.Fatal("one machine asked twice gave two names")
	}
}

// A machine that found nothing says so rather than quietly naming itself after nothing at all.
func TestAMachineThatFoundNothingHasNoName(t *testing.T) {
	var none Mark
	if none.Held() {
		t.Fatal("a machine that read nothing thinks it has something")
	}
	if _, err := none.Seed(); err == nil {
		t.Fatal("a machine that read nothing made a name anyway")
	}
	if none.Brief() != "" {
		t.Fatalf("a machine that read nothing shows %q", none.Brief())
	}
}

// The material itself is what the key is made from, so it must not be what is shown.
func TestWhatIsShownIsNotWhatTheKeyIsMadeFrom(t *testing.T) {
	secret := "S4EWNX0R540114Y"
	m := Mark{From: Disk, raw: []byte(secret)}

	if strings.Contains(m.Brief(), secret) {
		t.Fatalf("the serial is in what gets printed: %q", m.Brief())
	}
	seed, err := m.Seed()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(seed[:]), secret) {
		t.Fatal("the serial is in the seed as plain text")
	}
}

// laid builds a piece of sysfs the way the kernel lays one out.
func laid(t *testing.T, at string, files map[string]string) {
	t.Helper()

	for name, body := range files {
		full := filepath.Join(at, name)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// A drive is named by the serial written on it.
func TestAPlainDriveIsFoundByItsSerial(t *testing.T) {
	dir := t.TempDir()
	laid(t, dir, map[string]string{
		"dev":           "259:0\n",
		"device/serial": "S4EWNX0R540114Y\n",
		"device/model":  "Samsung SSD 990\n",
	})

	got := beneath(dir, deep)
	if len(got) != 1 || !strings.Contains(got[0], "S4EWNX0R540114Y") {
		t.Fatalf("a drive with a serial on it came back as %v", got)
	}
	if !strings.Contains(got[0], "Samsung") {
		t.Fatalf("the make of the drive was dropped: %v", got)
	}
}

// The root filesystem is on a partition, and a partition has no serial: the drive it was cut from
// does. Getting this wrong is how a machine ends up with no name on every ordinary PC.
func TestAPartitionIsFoundByTheDriveItWasCutFrom(t *testing.T) {
	dir := t.TempDir()
	laid(t, dir, map[string]string{
		"nvme0n1/dev":           "259:0\n",
		"nvme0n1/device/serial": "S4EWNX0R540114Y\n",
		"nvme0n1/nvme0n1p2/dev": "259:2\n",
	})

	got := beneath(filepath.Join(dir, "nvme0n1", "nvme0n1p2"), deep)
	if len(got) != 1 || !strings.Contains(got[0], "S4EWNX0R540114Y") {
		t.Fatalf("a partition came back as %v, and its drive has a serial", got)
	}
}

// Encryption on top of a volume group on top of a partition: the walk has to go all the way down
// to the metal, because everything above it is made up by the kernel and changes on a reinstall.
func TestTheWalkGoesThroughWhateverIsStackedOnTheDrive(t *testing.T) {
	dir := t.TempDir()
	laid(t, dir, map[string]string{
		"nvme0n1/dev":           "259:0\n",
		"nvme0n1/device/serial": "S4EWNX0R540114Y\n",
		"nvme0n1/nvme0n1p2/dev": "259:2\n",
		"dm-0/dev":              "253:0\n",
		"dm-1/dev":              "253:1\n",
	})
	// dm-1 (the encrypted volume) sits on dm-0, which sits on the partition.
	if err := os.MkdirAll(filepath.Join(dir, "dm-1", "slaves"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(dir, "dm-0"), filepath.Join(dir, "dm-1", "slaves", "dm-0")); err != nil {
		t.Skipf("this disk will not make a symlink: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "dm-0", "slaves"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(dir, "nvme0n1", "nvme0n1p2"), filepath.Join(dir, "dm-0", "slaves", "nvme0n1p2")); err != nil {
		t.Fatal(err)
	}

	got := beneath(filepath.Join(dir, "dm-1"), deep)
	if len(got) != 1 || !strings.Contains(got[0], "S4EWNX0R540114Y") {
		t.Fatalf("a stack three deep came back as %v", got)
	}
}

// A mirror is named by both of its halves, so losing one at boot does not rename the machine.
func TestAMirrorIsNamedByEveryDriveUnderIt(t *testing.T) {
	dir := t.TempDir()
	laid(t, dir, map[string]string{
		"sda/dev":           "8:0\n",
		"sda/device/serial": "AAA111\n",
		"sdb/dev":           "8:16\n",
		"sdb/device/serial": "BBB222\n",
		"md0/dev":           "9:0\n",
	})
	if err := os.MkdirAll(filepath.Join(dir, "md0", "slaves"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"sda", "sdb"} {
		if err := os.Symlink(filepath.Join(dir, name), filepath.Join(dir, "md0", "slaves", name)); err != nil {
			t.Skipf("this disk will not make a symlink: %v", err)
		}
	}

	got := beneath(filepath.Join(dir, "md0"), deep)
	if len(got) != 2 {
		t.Fatalf("a mirror of two drives came back as %v", got)
	}
	both := gathered(got)
	if !strings.Contains(string(both), "AAA111") || !strings.Contains(string(both), "BBB222") {
		t.Fatalf("a mirror lost one of its drives: %q", both)
	}
}

// Something built on itself must end rather than run until the stack does.
func TestAStackThatLoopsGivesUpInsteadOfSpinning(t *testing.T) {
	dir := t.TempDir()
	laid(t, dir, map[string]string{"dm-0/dev": "253:0\n"})
	if err := os.MkdirAll(filepath.Join(dir, "dm-0", "slaves"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(dir, "dm-0"), filepath.Join(dir, "dm-0", "slaves", "dm-0")); err != nil {
		t.Skipf("this disk will not make a symlink: %v", err)
	}

	done := make(chan []string, 1)
	go func() { done <- beneath(filepath.Join(dir, "dm-0"), deep) }()
	select {
	case got := <-done:
		if len(got) != 0 {
			t.Fatalf("a stack that loops named a machine: %v", got)
		}
	case <-timeout():
		t.Fatal("a stack that loops never came back")
	}
}

// Halves of a sealed thing go back together, and what is not one is refused rather than half-read.
func TestSealedHalvesGoBackTogether(t *testing.T) {
	pub, priv := []byte("the public half"), []byte("the private half")

	back, rest, err := split(joined(pub, priv))
	if err != nil {
		t.Fatal(err)
	}
	if string(back) != string(pub) || string(rest) != string(priv) {
		t.Fatalf("came back as %q and %q", back, rest)
	}

	for _, bad := range [][]byte{{}, {1}, {0, 0, 0, 99}, {255, 255, 255, 255, 1}} {
		if _, _, err := split(bad); err == nil {
			t.Errorf("%v was read as something sealed", bad)
		}
	}
}

// timeout is how long a walk is given before it is called a loop.
func timeout() <-chan struct{} {
	done := make(chan struct{})
	go func() {
		time.Sleep(5 * time.Second)
		close(done)
	}()
	return done
}
