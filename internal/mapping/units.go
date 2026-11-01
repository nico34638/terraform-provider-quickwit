package mapping

import (
	"fmt"
	"math"
	"math/bits"
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

// durationUnits are the units of humantime 2.4 (the version Quickwit 0.9
// uses), in nanoseconds; months and years are 30.44 and 365.25 days.
var durationUnits = map[string]uint64{
	"nanos": 1, "nsec": 1, "ns": 1,
	"usec": 1e3, "us": 1e3, "µs": 1e3,
	"millis": 1e6, "msec": 1e6, "ms": 1e6,
	"seconds": 1e9, "second": 1e9, "secs": 1e9, "sec": 1e9, "s": 1e9,
	"minutes": 60e9, "minute": 60e9, "min": 60e9, "mins": 60e9, "m": 60e9,
	"hours": 3600e9, "hour": 3600e9, "hr": 3600e9, "hrs": 3600e9, "h": 3600e9,
	"days": 86400e9, "day": 86400e9, "d": 86400e9,
	"weeks": 604800e9, "week": 604800e9, "wk": 604800e9, "wks": 604800e9, "w": 604800e9,
	"months": 2_630_016e9, "month": 2_630_016e9, "M": 2_630_016e9,
	"years": 31_557_600e9, "year": 31_557_600e9, "yr": 31_557_600e9, "yrs": 31_557_600e9, "y": 31_557_600e9,
}

// ParseHumanDuration parses durations the way humantime::parse_duration
// (2.4) does: a sequence of `<number><unit>` ("2days", "1h 30min", "1.5h",
// "30 days"). Like humantime, whitespace is allowed inside numbers and
// between a number and its unit, and a fraction must amount to a whole
// number of seconds (hours and longer units) or nanoseconds (shorter units).
// Durations longer than time.Duration (about 292 years) are rejected.
func ParseHumanDuration(s string) (time.Duration, error) {
	if s == "0" { // the only unit-less duration humantime accepts
		return 0, nil
	}
	fail := func(format string, args ...any) (time.Duration, error) {
		return 0, fmt.Errorf("invalid duration %q: %s", s, fmt.Sprintf(format, args...))
	}
	rs := []rune(s)
	pos := 0
	// nextNumber skips whitespace and consumes the first digit of the next
	// number; ok is false at the end of the input.
	nextNumber := func() (digit uint64, ok bool, err error) {
		for ; pos < len(rs); pos++ {
			switch c := rs[pos]; {
			case isDigit(c):
				pos++
				return uint64(c - '0'), true, nil
			case !unicode.IsSpace(c):
				return 0, false, fmt.Errorf("invalid duration %q: expected a number at %q", s, string(rs[pos:]))
			}
		}
		return 0, false, nil
	}

	n, ok, err := nextNumber()
	if err != nil {
		return 0, err
	}
	if !ok {
		return fail("empty")
	}
	var total uint64 // nanoseconds
	for {
		// Number: integer part, then an optional fraction num/den.
		var num, den uint64 = 0, 1
		unitStart := len(rs) // no unit unless a letter follows
	number:
		for ; pos < len(rs); pos++ {
			switch c := rs[pos]; {
			case isDigit(c):
				if n, ok = mulAdd(n, uint64(c-'0')); !ok {
					return fail("number too large")
				}
			case unicode.IsSpace(c):
			case isUnitLetter(c):
				unitStart = pos
				break number
			case c == '.':
				for pos++; pos < len(rs); pos++ {
					c := rs[pos]
					if isUnitLetter(c) {
						unitStart = pos
						break
					}
					if !isDigit(c) && !unicode.IsSpace(c) {
						return fail("invalid character %q", c)
					}
					if isDigit(c) {
						if num, ok = mulAdd(num, uint64(c-'0')); !ok {
							return fail("number too large")
						}
						if den, ok = mul(den, 10); !ok {
							return fail("number too large")
						}
					}
				}
				if den == 1 {
					return fail("expected digits after the decimal point")
				}
				break number
			default:
				return fail("invalid character %q", c)
			}
		}

		// Unit: letters up to whitespace or the next number ("1h30m").
		pos = unitStart
		for ; pos < len(rs) && isUnitLetter(rs[pos]); pos++ {
		}
		unit := string(rs[unitStart:pos])
		if pos < len(rs) && !isDigit(rs[pos]) && !unicode.IsSpace(rs[pos]) {
			return fail("invalid character %q", rs[pos])
		}
		perUnit, known := durationUnits[unit]
		if !known {
			if unit == "" {
				return fail("missing unit after %d (e.g. `s`, `min`, `h`, `days`)", n)
			}
			return fail("unknown unit %q", unit)
		}

		part, ok := mul(n, perUnit)
		if !ok {
			return fail("duration too long")
		}
		if den > 1 {
			fracPart, err := fraction(num, den, perUnit)
			if err != nil {
				return fail("%s", err)
			}
			if part, ok = add(part, fracPart); !ok {
				return fail("duration too long")
			}
		}
		if total, ok = add(total, part); !ok || total > math.MaxInt64 {
			return fail("duration too long (at most about 292 years)")
		}

		if n, ok, err = nextNumber(); err != nil {
			return 0, err
		} else if !ok {
			return time.Duration(total), nil
		}
	}
}

// fraction returns num/den units in nanoseconds, computed like humantime:
// in seconds for hours and longer units, in nanoseconds otherwise, and only
// when the division is exact.
func fraction(num, den, perUnit uint64) (uint64, error) {
	const second = uint64(1e9)
	if perUnit == 1 {
		return 0, fmt.Errorf("nanoseconds cannot have a fraction")
	}
	scale, factor, resolution := perUnit, uint64(1), "nanoseconds"
	if perUnit >= 3600*second {
		scale, factor, resolution = perUnit/second, second, "seconds"
	}
	x, ok := mul(num, scale)
	if !ok || x%den != 0 {
		return 0, fmt.Errorf("fraction too precise: it must amount to a whole number of %s", resolution)
	}
	r, ok := mul(x/den, factor)
	if !ok {
		return 0, fmt.Errorf("duration too long")
	}
	return r, nil
}

func isDigit(c rune) bool      { return c >= '0' && c <= '9' }
func isUnitLetter(c rune) bool { return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == 'µ' }

func mul(a, b uint64) (uint64, bool) {
	hi, lo := bits.Mul64(a, b)
	return lo, hi == 0
}

func add(a, b uint64) (uint64, bool) {
	s, carry := bits.Add64(a, b, 0)
	return s, carry == 0
}

// mulAdd returns a*10 + digit.
func mulAdd(a, digit uint64) (uint64, bool) {
	r, ok := mul(a, 10)
	if !ok {
		return 0, false
	}
	return add(r, digit)
}
