package cmd

import (
	"testing"
	"time"

	"github.com/bresilla/drop/src/pkg/book"
)

// A device that is switched off must not be dialled every fifteen seconds forever. Each attempt is
// a rendezvous lookup and a relay session, and the answer is not going to change quickly.
func TestADeviceThatDoesNotAnswerIsAskedLessOften(t *testing.T) {
	s := &staying{next: map[string]time.Time{}, wait: map[string]time.Duration{}}
	beta := book.Entry{Name: "beta"}

	if !s.due(beta) {
		t.Fatal("a device nobody has tried is not due")
	}

	var waits []time.Duration
	for range 8 {
		s.backOff(beta)
		waits = append(waits, s.wait[beta.Name])

		if s.due(beta) {
			t.Fatalf("a device that just failed is due again immediately (waited %v)", s.wait[beta.Name])
		}
	}

	// Doubling, and then stopping.
	for i := 1; i < len(waits); i++ {
		if waits[i] < waits[i-1] {
			t.Fatalf("the wait went backwards: %v", waits)
		}
	}
	if last := waits[len(waits)-1]; last != slowestRetry {
		t.Errorf("the wait settled at %v, want %v", last, slowestRetry)
	}
}

// And a device that answers is asked again as often as any other.
func TestADeviceThatAnswersIsNotHeldBack(t *testing.T) {
	s := &staying{next: map[string]time.Time{}, wait: map[string]time.Duration{}}
	beta := book.Entry{Name: "beta"}

	s.backOff(beta)
	if s.due(beta) {
		t.Fatal("a device that failed is due immediately")
	}

	// What reach does when a device answers.
	delete(s.next, beta.Name)
	delete(s.wait, beta.Name)

	if !s.due(beta) {
		t.Fatal("a device that answered is still being held back")
	}
}
