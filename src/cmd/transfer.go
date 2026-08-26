package cmd

import (
	"fmt"
	"sync"
	"time"

	"github.com/bresilla/drop/src/pkg/wire"
)

// progress prints one line per item, rewritten in place, at a rate a terminal can keep up with.
type progress struct {
	mu     sync.Mutex
	last   time.Time
	name   string
	active bool
}

func (p *progress) update(name string, done, total int64) {
	p.mu.Lock()
	defer p.mu.Unlock()

	now := time.Now()
	finished := total >= 0 && done >= total
	if !finished && now.Sub(p.last) < 100*time.Millisecond && name == p.name {
		return
	}
	p.last = now
	p.name = name
	p.active = true

	// An item with no length can only report what has arrived, because there is no total to be a
	// share of.
	if total == wire.SizeUnknown {
		fmt.Printf("\r  %-28s %s   ", name, bytes(done))
		return
	}

	share := 0.0
	if total > 0 {
		share = float64(done) / float64(total) * 100
	}
	fmt.Printf("\r  %-28s %s / %s  %5.1f%%   ", name, bytes(done), bytes(total), share)
	if finished {
		fmt.Println()
		p.active = false
	}
}

func (p *progress) clear() {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.active {
		fmt.Println()
		p.active = false
	}
}

// bytes renders a size the way a person reads it.
func bytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for size := n / unit; size >= unit; size /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}
