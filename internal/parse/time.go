package parse

import (
	"regexp"
	"strconv"
	"strings"
	"time"
)

// stringTimeLayouts are tried in order against string timestamp values.
// The list covers RFC3339 (Go/Rust/most JSON loggers), the space-separated
// form (Python logging, Java log4j/logback), and the comma-milliseconds
// variant Python's default formatter emits.
var stringTimeLayouts = []string{
	time.RFC3339Nano,
	time.RFC3339,
	"2006-01-02 15:04:05.000000",
	"2006-01-02 15:04:05.000",
	"2006-01-02 15:04:05,000",
	"2006-01-02 15:04:05",
	"2006/01/02 15:04:05",
}

// parseTimeString parses a timestamp string in any supported layout.
// Layouts without a zone are interpreted as UTC so identical inputs
// produce identical windows on every machine.
func parseTimeString(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, false
	}
	for _, layout := range stringTimeLayouts {
		if t, err := time.ParseInLocation(layout, s, time.UTC); err == nil {
			return t.UTC(), true
		}
	}
	// Bare epoch numbers also show up as strings ("ts": "1767225600").
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return parseEpoch(f)
	}
	return time.Time{}, false
}

// parseEpoch interprets a numeric timestamp, guessing the unit from its
// magnitude: seconds (~1e9 today), milliseconds (~1e12), microseconds
// (~1e15), or nanoseconds (~1e18). The heuristic holds for any date
// between 2001 and 2286, which covers every log file we care about.
func parseEpoch(v float64) (time.Time, bool) {
	if v <= 0 {
		return time.Time{}, false
	}
	switch {
	case v < 1e11: // seconds, possibly fractional
		sec := int64(v)
		nsec := int64((v - float64(sec)) * 1e9)
		return time.Unix(sec, nsec).UTC(), true
	case v < 1e14: // milliseconds
		return time.UnixMilli(int64(v)).UTC(), true
	case v < 1e17: // microseconds
		return time.UnixMicro(int64(v)).UTC(), true
	default: // nanoseconds
		return time.Unix(0, int64(v)).UTC(), true
	}
}

// leadingTimeRE matches a timestamp at the start of a plain-text line,
// optionally wrapped in brackets: RFC3339, or "YYYY-MM-DD HH:MM:SS" with
// optional fractional seconds (dot or comma separated) and optional zone.
var leadingTimeRE = regexp.MustCompile(
	`^\[?(\d{4}[-/]\d{2}[-/]\d{2}[T ]\d{2}:\d{2}:\d{2}(?:[.,]\d+)?(?:Z|[+-]\d{2}:?\d{2})?)\]?\s*`)

// stripLeadingTime removes a recognized timestamp prefix from a text line,
// returning the remainder and the parsed time.
func stripLeadingTime(line string) (rest string, t time.Time, ok bool) {
	m := leadingTimeRE.FindStringSubmatch(line)
	if m == nil {
		return line, time.Time{}, false
	}
	raw := strings.Replace(m[1], "T", " ", 1)
	// Re-attach the T for RFC3339 layouts; try both spellings.
	if t, ok = parseTimeString(m[1]); !ok {
		t, ok = parseTimeString(raw)
	}
	if !ok {
		return line, time.Time{}, false
	}
	return line[len(m[0]):], t, true
}
