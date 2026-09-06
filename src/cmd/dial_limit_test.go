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

func TestBoundedWorkSharesCapacityAcrossConnections(t *testing.T) {
	all := make(chan struct{}, 1)
	first := make(chan struct{}, 1)
	second := make(chan struct{}, 1)
	started := make(chan struct{})
	release := make(chan struct{})
	if !startBoundedWithin(first, all, func() {
		close(started)
		<-release
	}) {
		t.Fatal("the first stream was refused")
	}
	<-started
	if startBoundedWithin(second, all, func() {}) {
		t.Fatal("another connection crossed the shared stream limit")
	}
	close(release)

	until := time.Now().Add(time.Second)
	for len(all) != 0 && time.Now().Before(until) {
		time.Sleep(time.Millisecond)
	}
	accepted := make(chan struct{})
	if !startBoundedWithin(second, all, func() { close(accepted) }) {
		t.Fatal("a stream stayed refused after shared capacity returned")
	}
	select {
	case <-accepted:
	case <-time.After(time.Second):
		t.Fatal("accepted stream did not run")
	}
}

func TestOneConnectionCannotConsumeEveryStreamSlot(t *testing.T) {
	if maxStreamsPerConnection >= maxServingStreams {
		t.Fatalf("one connection may consume %d of %d stream slots", maxStreamsPerConnection, maxServingStreams)
	}
}
