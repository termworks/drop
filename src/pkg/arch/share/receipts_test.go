package share

import (
	"testing"
	"time"
)

func TestReceiptIdentityCannotNameDifferentMetadata(t *testing.T) {
	instance := newConfigInstance()
	key := receiptKey{transfer: transferID{1}}
	item := Item{Name: "one", Size: 3, Mode: 0o600}
	instance.remember(key, item, "one", 3, [32]byte{1})

	if _, found, err := instance.lookup(key, item); err != nil || !found {
		t.Fatalf("matching receipt = %v, %v", found, err)
	}
	other := item
	other.Name = "two"
	if _, _, err := instance.lookup(key, other); err == nil {
		t.Fatal("receipt identity accepted different item metadata")
	}
}

func TestReceiptReportingHappensOnce(t *testing.T) {
	instance := newConfigInstance()
	key := receiptKey{transfer: transferID{1}}
	instance.remember(key, Item{Name: "one"}, "one", 3, [32]byte{1})
	if !instance.report(key) {
		t.Fatal("new receipt was already reported")
	}
	if instance.report(key) {
		t.Fatal("receipt was reported twice")
	}
}

func TestExpiredReceiptsAreNotReplayed(t *testing.T) {
	instance := newConfigInstance()
	key := receiptKey{transfer: transferID{1}}
	item := Item{Name: "one"}
	instance.remember(key, item, "one", 3, [32]byte{1})
	instance.receipts[key].expires = time.Now().Add(-time.Second)

	if _, found, err := instance.lookup(key, item); err != nil || found {
		t.Fatalf("expired receipt = %v, %v", found, err)
	}
}

func TestReceiptMemoryIsBounded(t *testing.T) {
	instance := newConfigInstance()
	item := Item{Name: "one"}
	for i := range maxReceipts + 1 {
		key := receiptKey{transfer: transferID{1}, item: uint32(i)}
		instance.remember(key, item, "one", 1, [32]byte{1})
	}
	if len(instance.receipts) != maxReceipts || instance.order.Len() != maxReceipts {
		t.Fatalf("kept %d receipts and %d order entries", len(instance.receipts), instance.order.Len())
	}
	if _, found, err := instance.lookup(receiptKey{transfer: transferID{1}}, item); err != nil || found {
		t.Fatalf("oldest receipt survived the bound: %v, %v", found, err)
	}
}
