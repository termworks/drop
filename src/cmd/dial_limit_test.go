package cmd

import (
	"sync"
	"testing"
	"time"
)

func TestBoundedWorkRefusesPastItsCapacity(t *testing.T) {
	slots := make(chan struct{}, 2)
	release := make(chan struct{})
	started := make(chan struct{}, 2)
	var done sync.WaitGroup
	done.Add(2)
	work := func() {
		started <- struct{}{}
		<-release
		done.Done()
	}

	for range 2 {
		if !startBounded(slots, work) {
			t.Fatal("work was refused before the limit")
		}
	}
	<-started
	<-started
	if startBounded(slots, func() {}) {
		t.Fatal("work was accepted past the limit")
	}
	close(release)
	done.Wait()

	accepted := make(chan struct{})
	if !startBounded(slots, func() { close(accepted) }) {
		t.Fatal("work stayed refused after capacity returned")
	}
	select {
	case <-accepted:
	case <-time.After(time.Second):
		t.Fatal("accepted work did not run")
	}
}
