package share

import (
	"container/list"
	"fmt"
	"sync"
	"time"

	"github.com/bresilla/drop/src/pkg/node"
)

const (
	maxReceipts     = maxItems
	receiptLifetime = 10 * time.Minute
)

type receiptKey struct {
	from     node.ID
	transfer transferID
	item     uint32
}

type receipt struct {
	item     Item
	name     string
	size     int64
	digest   [32]byte
	reported bool
	expires  time.Time
	order    *list.Element
}

type configInstance struct {
	mu       sync.Mutex
	receipts map[receiptKey]*receipt
	order    list.List
}

func newConfigInstance() *configInstance {
	return &configInstance{receipts: map[receiptKey]*receipt{}}
}

func (c *configInstance) lookup(key receiptKey, item Item) (receipt, bool, error) {
	if c == nil {
		return receipt{}, false, nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	stored, ok := c.receipts[key]
	if !ok {
		return receipt{}, false, nil
	}
	if time.Now().After(stored.expires) {
		c.remove(key, stored)
		return receipt{}, false, nil
	}
	if stored.item != item {
		return receipt{}, false, fmt.Errorf("transfer identity was already used for a different item")
	}
	stored.expires = time.Now().Add(receiptLifetime)
	c.order.MoveToBack(stored.order)
	return *stored, true, nil
}

func (c *configInstance) remember(key receiptKey, item Item, name string, size int64, digest [32]byte) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	if stored, ok := c.receipts[key]; ok {
		stored.item, stored.name, stored.size, stored.digest = item, name, size, digest
		stored.expires = time.Now().Add(receiptLifetime)
		c.order.MoveToBack(stored.order)
		return
	}
	for len(c.receipts) >= maxReceipts {
		front := c.order.Front()
		if front == nil {
			break
		}
		old := front.Value.(receiptKey)
		c.remove(old, c.receipts[old])
	}
	stored := &receipt{item: item, name: name, size: size, digest: digest, expires: time.Now().Add(receiptLifetime)}
	stored.order = c.order.PushBack(key)
	c.receipts[key] = stored
}

func (c *configInstance) report(key receiptKey) bool {
	if c == nil {
		return true
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	stored, ok := c.receipts[key]
	if !ok || stored.reported {
		return false
	}
	stored.reported = true
	return true
}

func (c *configInstance) remove(key receiptKey, stored *receipt) {
	delete(c.receipts, key)
	if stored != nil && stored.order != nil {
		c.order.Remove(stored.order)
	}
}
