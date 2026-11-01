package mapping

import (
	"strings"
	"testing"
	"time"
)

// Expected results come from humantime 2.4 (humantime::parse_duration).
func TestParseHumanDuration_humantime(t *testing.T) {
	valid := map[string]time.Duration{
		"0":       0,
		"1.5h":    90 * time.Minute,
		"0.5s":    500 * time.Millisecond,
		"1 0s":    10 * time.Second, // whitespace inside numbers is ignored
		"1h30m":   90 * time.Minute,
		"30 days": 30 * 24 * time.Hour,
		"2wk":     14 * 24 * time.Hour,
		"3 yrs":   94_672_800 * time.Second,
		"1µs":     time.Microsecond,
		"1M":      2_630_016 * time.Second,
	}
	for in, want := range valid {
		if got, err := ParseHumanDuration(in); err != nil || got != want {
			t.Errorf("ParseHumanDuration(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	invalid := map[string]string{
		"":         "empty",
		"10":       "missing unit",
		"1h 30":    "missing unit",
		"1h,30m":   "invalid character",
		"1micros":  `unknown unit "micros"`,
		"1.h":      "decimal point",
		"1.3333h":  "fraction too precise",
		"1.5ns":    "fraction",
		"-1h":      "expected a number",
		"300years": "too long",
	}
	for in, want := range invalid {
		if _, err := ParseHumanDuration(in); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("ParseHumanDuration(%q) error = %v; want it to contain %q", in, err, want)
		}
	}
}
