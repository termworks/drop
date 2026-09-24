package share

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/bresilla/drop/src/pkg/arch"
)

const (
	defaultMaxItemBytes    int64 = 4 << 30
	defaultMaxSessionBytes int64 = 16 << 30
)

type transferQuota struct {
	item    int64
	session int64
	used    int64
}

func quotaFor(cfg Config) transferQuota {
	item, session := cfg.MaxItemBytes, cfg.MaxSessionBytes
	if item <= 0 {
		item = defaultMaxItemBytes
	}
	if session <= 0 {
		session = defaultMaxSessionBytes
	}
	return transferQuota{item: item, session: session}
}

func (q *transferQuota) preflight(items []Item, picked resume) string {
	remaining := int64(0)
	q.used = 0
	for i, item := range items {
		if picked.At[i] > q.item {
			return fmt.Sprintf("a partial item exceeds the %d-byte item limit", q.item)
		}
		if picked.At[i] > q.session-q.used {
			return fmt.Sprintf("the offer already exceeds the %d-byte session limit", q.session)
		}
		q.used += picked.At[i]
		if picked.Done[i] {
			continue
		}
		if !item.Known() {
			continue
		}
		if item.Size > q.item {
			return fmt.Sprintf("an item claims %d bytes, over the %d-byte item limit", item.Size, q.item)
		}
		left := item.Size - picked.At[i]
		if left > q.session-q.used-remaining {
			return fmt.Sprintf("the offer exceeds the %d-byte session limit", q.session)
		}
		remaining += left
	}
	return ""
}

func (q *transferQuota) take(item, next int64) error {
	if next > q.item-item {
		return fmt.Errorf("the item crossed the %d-byte item limit", q.item)
	}
	if next > q.session-q.used {
		return fmt.Errorf("the transfer crossed the %d-byte session limit", q.session)
	}
	q.used += next
	return nil
}

func configuredLimit(d arch.Declared, key string) (int64, error) {
	text, set := d.String(key)
	if !set {
		if _, mentioned := d.Bool(key); mentioned {
			return 0, fmt.Errorf("%s must be a byte size written as text", key)
		}
		return 0, nil
	}

	limit, err := parseByteLimit(text)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", key, err)
	}
	return limit, nil
}

func parseByteLimit(text string) (int64, error) {
	text = strings.TrimSpace(text)
	at := 0
	for at < len(text) && text[at] >= '0' && text[at] <= '9' {
		at++
	}
	if at == 0 {
		return 0, fmt.Errorf("%q is not a byte size", text)
	}

	n, err := strconv.ParseInt(text[:at], 10, 64)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("%q is not a positive byte size", text)
	}
	units := map[string]int64{
		"": 1, "B": 1, "KIB": 1 << 10, "MIB": 1 << 20, "GIB": 1 << 30, "TIB": 1 << 40,
	}
	unit := strings.ToUpper(strings.TrimSpace(text[at:]))
	multiple, ok := units[unit]
	if !ok {
		return 0, fmt.Errorf("%q uses an unknown byte unit", text)
	}
	if n > math.MaxInt64/multiple {
		return 0, fmt.Errorf("%q is too large", text)
	}
	return n * multiple, nil
}
