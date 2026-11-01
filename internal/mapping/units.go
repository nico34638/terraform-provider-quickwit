package mapping

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// ParseByteSize parses sizes the way the `bytesize` crate does: a number
// followed by an optional unit (B, KB, KiB, MB, MiB…, case-insensitive).
func ParseByteSize(s string) (uint64, error) {
	s = strings.TrimSpace(s)
	i := strings.IndexFunc(s, func(r rune) bool { return !unicode.IsDigit(r) && r != '.' })
	num, unit := s, ""
	if i >= 0 {
		num, unit = s[:i], strings.ToLower(strings.TrimSpace(s[i:]))
	}
	v, err := strconv.ParseFloat(num, 64)
	if err != nil || num == "" {
		return 0, fmt.Errorf("invalid byte size %q", s)
	}
	mult := map[string]float64{
		"": 1, "b": 1,
		"k": 1e3, "kb": 1e3, "ki": 1 << 10, "kib": 1 << 10,
		"m": 1e6, "mb": 1e6, "mi": 1 << 20, "mib": 1 << 20,
		"g": 1e9, "gb": 1e9, "gi": 1 << 30, "gib": 1 << 30,
		"t": 1e12, "tb": 1e12, "ti": 1 << 40, "tib": 1 << 40,
		"p": 1e15, "pb": 1e15, "pi": 1 << 50, "pib": 1 << 50,
	}[unit]
	if mult == 0 {
		return 0, fmt.Errorf("invalid byte size %q: unknown unit %q", s, unit)
	}
	return uint64(math.Round(v * mult)), nil
}

// ParseHumanDuration parses durations the way the `humantime` crate does,
// e.g. "2days", "1day 12h", "30s", "1h 30min".
func ParseHumanDuration(s string) (time.Duration, error) {
	units := map[string]float64{
		"nanos": 1, "nsec": 1, "ns": 1,
		"usec": 1e3, "us": 1e3, "micros": 1e3,
		"millis": 1e6, "msec": 1e6, "ms": 1e6,
		"seconds": 1e9, "second": 1e9, "secs": 1e9, "sec": 1e9, "s": 1e9,
		"minutes": 60e9, "minute": 60e9, "mins": 60e9, "min": 60e9, "m": 60e9,
		"hours": 3600e9, "hour": 3600e9, "hrs": 3600e9, "hr": 3600e9, "h": 3600e9,
		"days": 86400e9, "day": 86400e9, "d": 86400e9,
		"weeks": 7 * 86400e9, "week": 7 * 86400e9, "w": 7 * 86400e9,
		"months": 2_630_016e9, "month": 2_630_016e9, "M": 2_630_016e9,
		"years": 31_557_600e9, "year": 31_557_600e9, "y": 31_557_600e9,
	}
	rest := strings.TrimSpace(s)
	if rest == "" {
		return 0, fmt.Errorf("invalid duration %q: empty", s)
	}
	var total float64
	for rest != "" {
		i := strings.IndexFunc(rest, func(r rune) bool { return !unicode.IsDigit(r) })
		if i <= 0 {
			return 0, fmt.Errorf("invalid duration %q: expected a number", s)
		}
		n, _ := strconv.ParseFloat(rest[:i], 64)
		rest = strings.TrimLeft(rest[i:], " ")
		j := strings.IndexFunc(rest, func(r rune) bool { return !unicode.IsLetter(r) })
		if j < 0 {
			j = len(rest)
		}
		unit := rest[:j]
		mult, ok := units[unit]
		if !ok {
			return 0, fmt.Errorf("invalid duration %q: unknown unit %q", s, unit)
		}
		total += n * mult
		rest = strings.TrimLeft(rest[j:], " ")
	}
	return time.Duration(total), nil
}
