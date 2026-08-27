package user

import (
	"strings"
	"testing"
	"time"
)

// A badge is bytes somebody signed, and it is read back as exactly those bytes or not at all.
//
// Forgiving a line that was not written by the signer is how a name with a newline in it becomes a
// device line that parse then honours over the real one.
func TestABadgeIsReadBackOrRefused(t *testing.T) {
	by := aKey(t)
	now := time.Now()

	good, sig, err := Sign(by, someDevice, "laptop", now)
	if err != nil {
		t.Fatalf("Sign(): %v", err)
	}

	for what, written := range map[string]string{
		"a line nobody knows": string(good.Bytes()) + "colour blue\n",
		"a keyword said twice": strings.Replace(string(good.Bytes()),
			"name laptop\n", "name laptop\nname workstation\n", 1),
		"a device said twice": strings.Replace(string(good.Bytes()),
			"device "+someDevice+"\n", "device "+someDevice+"\ndevice "+strings.Repeat("a", 64)+"\n", 1),
	} {
		if _, err := parse([]byte(written)); err == nil {
			t.Errorf("%s was read as a badge", what)
		}
	}

	// The one that was actually signed still reads.
	if _, err := Read(good.Bytes(), sig, now); err != nil {
		t.Fatalf("a good badge was refused: %v", err)
	}
}

// A name that spans lines is a badge that says something other than what it appears to say, so it
// never gets signed in the first place.
func TestAFieldThatSpansLinesCannotBeSigned(t *testing.T) {
	by := aKey(t)
	now := time.Now()

	if _, _, err := Sign(by, someDevice, "laptop\ndevice "+strings.Repeat("b", 64), now); err == nil {
		t.Error("a name carrying a whole line was signed")
	}
	if _, _, err := Sign(by, someDevice+"\nname elsewhere", "laptop", now); err == nil {
		t.Error("a device carrying a whole line was signed")
	}
}

// Signing such a badge by hand does not help either: the signature covers bytes that do not read
// back as the badge they were written from.
func TestASmuggledLineIsNotBelieved(t *testing.T) {
	by := aKey(t)
	now := time.Now()

	smuggled := Badge{
		User:   by.PublicKey(),
		Device: someDevice,
		Name:   "laptop\ndevice " + strings.Repeat("c", 64),
		Until:  now.Add(Lasts),
	}
	sig, err := Signature(by, smuggled.Bytes())
	if err != nil {
		t.Fatal(err)
	}

	if _, err := Read(smuggled.Bytes(), sig, now); err == nil {
		t.Fatal("a badge with a line smuggled into its name was believed")
	}
}
