package user

import (
	"bytes"
	"testing"
)

func TestTwoMachinesWorkOutOneSecretForTheirPair(t *testing.T) {
	circle := bytes.Repeat([]byte{7}, CircleSize)
	if !bytes.Equal(PairSecret(circle, "phone", "laptop"), PairSecret(circle, "laptop", "phone")) {
		t.Fatal("the two ends of a pair worked out different secrets")
	}
	if bytes.Equal(PairSecret(circle, "phone", "laptop"), PairSecret(circle, "phone", "desk")) {
		t.Fatal("two pairs share a secret")
	}
}

func TestTwoCirclesSettleOnTheLower(t *testing.T) {
	aMachine(t)
	made, err := MakeCircle()
	if err != nil {
		t.Fatal(err)
	}
	again, err := MakeCircle()
	if err != nil || !bytes.Equal(made, again) {
		t.Fatalf("a second ask made another circle: %v", err)
	}

	higher := bytes.Repeat([]byte{0xff}, CircleSize)
	if changed, err := AdoptCircle(higher); err != nil || changed {
		t.Fatalf("a higher circle was adopted: %v, %v", changed, err)
	}
	lower := make([]byte, CircleSize)
	if changed, err := AdoptCircle(lower); err != nil || !changed {
		t.Fatalf("the lower circle was not adopted: %v, %v", changed, err)
	}
	if held, _ := Circle(); !bytes.Equal(held, lower) {
		t.Fatal("the adopted circle is not the one held")
	}
}
