package files

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/bresilla/drop/src/pkg/arch"
	"github.com/bresilla/drop/src/pkg/wire"
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

func (q *transferQuota) preflight(size int64) string {
	if size == wire.SizeUnknown {
		return ""
	}
	if size > q.item {
		return fmt.Sprintf("the item claims %d bytes, over the %d-byte item limit", size, q.item)
	}
	if size > q.session-q.used {
		return fmt.Sprintf("the item claims %d bytes, over the %d bytes left in this session", size, q.session-q.used)
	}
	return ""
}

func (q *transferQuota) take(name string, item, next int64) error {
	if next > q.item-item {
		return fmt.Errorf("%s crossed the %d-byte item limit", name, q.item)
	}
	if next > q.session-q.used {
		return fmt.Errorf("%s crossed the %d-byte session limit", name, q.session)
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
