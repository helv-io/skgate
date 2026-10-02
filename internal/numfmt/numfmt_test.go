package numfmt

import (
	"math"
	"testing"
)

func TestCompact(t *testing.T) {
	for in, want := range map[int64]string{
		0: "0", 7: "7", 999: "999",
		1000: "1K", 1049: "1K", 1050: "1.1K", 1234: "1.2K", 9949: "9.9K", 9950: "10K", 9999: "10K",
		12345: "12K", 99499: "99K", 123456: "123K", 999499: "999K",
		999500: "1M", 999999: "1M", 1_000_000: "1M", 1_234_567: "1.2M", 12_345_678: "12M", 340_000: "340K",
		999_999_999: "1B", 1_000_000_000: "1B", 2_500_000_000: "2.5B",
		999_999_999_999: "1T", 1_000_000_000_000: "1T", 123_456_789_012_345: "123T",
		1_000_000_000_000_000: "1Qa", 1_000_000_000_000_000_000: "1Qi", math.MaxInt64: "9.2Qi",
		-1234: "-1.2K", -999: "-999",
	} {
		if got := Compact(in); got != want {
			t.Errorf("Compact(%d) = %q, want %q", in, got, want)
		}
	}
	if got := Compact(math.MinInt64); got != "-9.2Qi" {
		t.Errorf("MinInt64: %q", got)
	}
}

func TestExact(t *testing.T) {
	for in, want := range map[int64]string{0: "0", 999: "999", 1000: "1,000", 1234567: "1,234,567", -12345: "-12,345", 100000: "100,000"} {
		if got := Exact(in); got != want {
			t.Errorf("Exact(%d) = %q, want %q", in, got, want)
		}
	}
}
