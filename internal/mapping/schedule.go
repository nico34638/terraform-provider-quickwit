package mapping

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
)

// ValidateRetentionSchedule checks a retention evaluation schedule the way
// Quickwit 0.9 does (RetentionPolicy::evaluation_schedule): a bare word gets
// an `@` prefix, then the result must parse with the `cron` crate 0.17:
//
//   - a shorthand: `@hourly`, `@daily`, `@weekly`, `@monthly`, `@yearly`;
//   - or 6 or 7 fields: second minute hour day-of-month month day-of-week
//     [year], each a `,`-separated list of `*`, `N`, `N-M`, names (`jan`,
//     `mon`… for months and days of week), with an optional `/step`; `?` is
//     accepted for the days of month and of week.
func ValidateRetentionSchedule(s string) error {
	expr := strings.TrimSpace(s)
	if expr != "" && !strings.HasPrefix(expr, "@") && isASCIIAlpha(expr) {
		expr = "@" + expr
	}
	if err := parseCron(expr); err != nil {
		return fmt.Errorf("invalid schedule %q: %w", s, err)
	}
	return nil
}

var cronShorthands = []string{"@yearly", "@monthly", "@weekly", "@daily", "@hourly"}

type cronUnit struct {
	name     string
	min, max uint32
	names    map[string]uint32
	allowAny bool // `?`
}

var cronUnits = []cronUnit{
	{name: "seconds", min: 0, max: 59},
	{name: "minutes", min: 0, max: 59},
	{name: "hours", min: 0, max: 23},
	{name: "days of month", min: 1, max: 31, allowAny: true},
	{name: "months", min: 1, max: 12, names: map[string]uint32{
		"jan": 1, "january": 1, "feb": 2, "february": 2, "mar": 3, "march": 3, "apr": 4, "april": 4,
		"may": 5, "jun": 6, "june": 6, "jul": 7, "july": 7, "aug": 8, "august": 8,
		"sep": 9, "september": 9, "oct": 10, "october": 10, "nov": 11, "november": 11, "dec": 12, "december": 12,
	}},
	{name: "days of week", min: 1, max: 7, allowAny: true, names: map[string]uint32{
		"sun": 1, "sunday": 1, "mon": 2, "monday": 2, "tue": 3, "tues": 3, "tuesday": 3,
		"wed": 4, "wednesday": 4, "thu": 5, "thurs": 5, "thursday": 5, "fri": 6, "friday": 6, "sat": 7, "saturday": 7,
	}},
	{name: "years", min: 1970, max: 2100},
}

func parseCron(expr string) error {
	p := &cronParser{s: expr}
	p.ws()
	for _, sh := range cronShorthands {
		if strings.HasPrefix(p.s[p.pos:], sh) {
			p.pos += len(sh)
			p.ws()
			if !p.eof() {
				return fmt.Errorf("unexpected %q after %s", p.s[p.pos:], sh)
			}
			return nil
		}
	}
	if strings.HasPrefix(p.s[p.pos:], "@") {
		return fmt.Errorf("unknown shorthand, expected one of hourly, daily, weekly, monthly, yearly")
	}

	for i, unit := range cronUnits {
		specs, ok := p.field(unit.allowAny)
		if !ok {
			if i == len(cronUnits)-1 { // the year is optional
				break
			}
			if p.eof() {
				return fmt.Errorf("expected 6 or 7 fields (second minute hour day-of-month month day-of-week [year]), got %d", i)
			}
			return fmt.Errorf("invalid %s field at %q", unit.name, p.s[p.pos:])
		}
		if err := unit.validate(specs); err != nil {
			return fmt.Errorf("%s: %w", unit.name, err)
		}
	}
	if !p.eof() {
		return fmt.Errorf("unexpected %q after the fields", p.s[p.pos:])
	}
	return nil
}

func isASCIIAlpha(s string) bool {
	for _, c := range s {
		if c > unicode.MaxASCII || !unicode.IsLetter(c) {
			return false
		}
	}
	return true
}

// cronSpec is a RootSpecifier of the cron crate.
type cronSpec struct {
	kind     cronKind
	a, b     uint32 // point, range
	na, nb   string // named point, named range
	start    *cronSpec
	step     uint32
	original string
}

type cronKind int

const (
	cronAll cronKind = iota
	cronPoint
	cronRange
	cronNamedRange
	cronNamedPoint
	cronPeriod
)

func (u cronUnit) validate(specs []cronSpec) error {
	if len(specs) == 1 && specs[0].kind == cronAll {
		return nil
	}
	for _, sp := range specs {
		if err := u.validateSpec(sp); err != nil {
			return err
		}
	}
	return nil
}

func (u cronUnit) validateSpec(sp cronSpec) error {
	switch sp.kind {
	case cronAll:
		return nil
	case cronPoint:
		return u.check(sp.a)
	case cronNamedPoint:
		n, err := u.ordinal(sp.na)
		if err != nil {
			return err
		}
		return u.check(n)
	case cronRange, cronNamedRange:
		a, b := sp.a, sp.b
		if sp.kind == cronNamedRange {
			var err error
			if a, err = u.ordinal(sp.na); err != nil {
				return err
			}
			if b, err = u.ordinal(sp.nb); err != nil {
				return err
			}
		}
		if u.check(a) != nil || u.check(b) != nil || a > b {
			return fmt.Errorf("invalid range %s", sp.original)
		}
		return nil
	case cronPeriod:
		if sp.step < 1 || sp.step > u.max {
			return fmt.Errorf("step must be between 1 and %d (%d specified)", u.max, sp.step)
		}
		return u.validateSpec(*sp.start)
	}
	return nil
}

func (u cronUnit) check(n uint32) error {
	if n < u.min || n > u.max {
		return fmt.Errorf("%d is out of range %d-%d", n, u.min, u.max)
	}
	return nil
}

func (u cronUnit) ordinal(name string) (uint32, error) {
	if u.names == nil {
		return 0, fmt.Errorf("names are not supported (%q specified)", name)
	}
	n, ok := u.names[strings.ToLower(name)]
	if !ok {
		return 0, fmt.Errorf("%q is not a valid name", name)
	}
	return n, nil
}

// cronParser mirrors the winnow grammar of cron 0.17 (parsing.rs), including
// where whitespace is allowed (around numbers, names and fields). Each parse
// method restores the position when it fails.
type cronParser struct {
	s   string
	pos int
}

func (p *cronParser) eof() bool { return p.pos >= len(p.s) }

func (p *cronParser) ws() {
	for !p.eof() && strings.IndexByte(" \t\r\n", p.s[p.pos]) >= 0 {
		p.pos++
	}
}

func (p *cronParser) lit(c byte) bool {
	if !p.eof() && p.s[p.pos] == c {
		p.pos++
		return true
	}
	return false
}

// token consumes whitespace, a run of characters matching class, whitespace.
func (p *cronParser) token(class func(byte) bool) (string, bool) {
	start := p.pos
	p.ws()
	i := p.pos
	for !p.eof() && class(p.s[p.pos]) {
		p.pos++
	}
	if p.pos == i {
		p.pos = start
		return "", false
	}
	tok := p.s[i:p.pos]
	p.ws()
	return tok, true
}

func (p *cronParser) ordinal() (uint32, bool) {
	start := p.pos
	tok, ok := p.token(func(c byte) bool { return c >= '0' && c <= '9' })
	if !ok {
		return 0, false
	}
	n, err := strconv.ParseUint(tok, 10, 32)
	if err != nil {
		p.pos = start
		return 0, false
	}
	return uint32(n), true
}

func (p *cronParser) name() (string, bool) {
	return p.token(func(c byte) bool { return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' })
}

// specifier: `?` (if allowed), `*`, `N-M`, `N`, `name-name`.
func (p *cronParser) specifier(allowAny bool) (cronSpec, bool) {
	start := p.pos
	if allowAny && p.lit('?') || p.lit('*') {
		return cronSpec{kind: cronAll}, true
	}
	if a, ok := p.ordinal(); ok {
		if p.lit('-') {
			if b, ok := p.ordinal(); ok {
				return cronSpec{kind: cronRange, a: a, b: b, original: strings.TrimSpace(p.s[start:p.pos])}, true
			}
		}
		p.pos = start
		a, _ = p.ordinal()
		return cronSpec{kind: cronPoint, a: a}, true
	}
	if na, ok := p.name(); ok {
		if p.lit('-') {
			if nb, ok := p.name(); ok {
				return cronSpec{kind: cronNamedRange, na: na, nb: nb, original: strings.TrimSpace(p.s[start:p.pos])}, true
			}
		}
	}
	p.pos = start
	return cronSpec{}, false
}

// rootSpecifier: `specifier/step`, `specifier`, `name`.
func (p *cronParser) rootSpecifier(allowAny bool) (cronSpec, bool) {
	start := p.pos
	if sp, ok := p.specifier(allowAny); ok {
		afterSpec := p.pos
		if p.lit('/') {
			if step, ok := p.ordinal(); ok {
				return cronSpec{kind: cronPeriod, start: &sp, step: step}, true
			}
		}
		p.pos = afterSpec
		return sp, true
	}
	if na, ok := p.name(); ok {
		return cronSpec{kind: cronNamedPoint, na: na}, true
	}
	p.pos = start
	return cronSpec{}, false
}

// field: whitespace, a `,`-separated list of root specifiers, whitespace.
func (p *cronParser) field(allowAny bool) ([]cronSpec, bool) {
	start := p.pos
	p.ws()
	first, ok := p.rootSpecifier(allowAny)
	if !ok {
		p.pos = start
		return nil, false
	}
	specs := []cronSpec{first}
	for {
		beforeSep := p.pos
		if !p.lit(',') {
			break
		}
		sp, ok := p.rootSpecifier(allowAny)
		if !ok {
			p.pos = beforeSep
			break
		}
		specs = append(specs, sp)
	}
	p.ws()
	return specs, true
}
