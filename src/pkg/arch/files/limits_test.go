package files

import "testing"

func TestByteLimitsUseBinaryUnits(t *testing.T) {
	for _, test := range []struct {
		text string
		want int64
	}{
		{"1", 1},
		{"2 B", 2},
		{"3 KiB", 3 << 10},
		{"4 MiB", 4 << 20},
		{"5 GiB", 5 << 30},
		{"6 TiB", 6 << 40},
	} {
		got, err := parseByteLimit(test.text)
		if err != nil || got != test.want {
			t.Errorf("parseByteLimit(%q) = %d, %v; want %d", test.text, got, err, test.want)
		}
	}
}

func TestInvalidByteLimitsAreRefused(t *testing.T) {
	for _, text := range []string{"", "0", "-1 MiB", "1 MB", "999999999999999999999 TiB"} {
		if got, err := parseByteLimit(text); err == nil {
			t.Errorf("parseByteLimit(%q) = %d", text, got)
		}
	}
}

func TestMissingLimitsUseBoundedDefaults(t *testing.T) {
	quota := quotaFor(Config{})
	if quota.item != defaultMaxItemBytes || quota.session != defaultMaxSessionBytes {
		t.Fatalf("quotaFor(Config{}) = %+v", quota)
	}
}
