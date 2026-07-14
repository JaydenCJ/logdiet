// Package template collapses log messages back to the statement that
// produced them. The core operation is Mask: replace every variable-looking
// token (numbers, IDs, IPs, durations, quoted strings, path segments…)
// with a typed placeholder, so the million renderings of one fmt string
// fold into a single template — the unit logdiet ranks and prices.
//
// Masking is rule-based and ordered: specific patterns (timestamps, UUIDs,
// IPs) run before greedy ones (hex runs, bare numbers) so a UUID becomes
// one <uuid>, not five <hex>/<n> fragments. Mask is idempotent —
// Mask(Mask(s)) == Mask(s) — which the test suite enforces, because
// aggregation keys must be stable no matter how often a string passes
// through.
package template

import (
	"hash/fnv"
	"regexp"
	"strings"
)

// rule is one masking pass: a compiled pattern and its placeholder.
type rule struct {
	re          *regexp.Regexp
	placeholder string
}

// The ordered rule set. Order is load-bearing; see the package comment.
var rules = []rule{
	// ISO-ish timestamps embedded mid-message ("retry at 2026-07-12T10:00:00Z").
	{regexp.MustCompile(`\b\d{4}-\d{2}-\d{2}[T ]\d{2}:\d{2}:\d{2}(?:[.,]\d+)?(?:Z|[+-]\d{2}:?\d{2})?\b`), "<ts>"},
	// Bare dates and clock times.
	{regexp.MustCompile(`\b\d{4}-\d{2}-\d{2}\b`), "<date>"},
	{regexp.MustCompile(`\b\d{2}:\d{2}:\d{2}(?:[.,]\d+)?\b`), "<time>"},
	// UUIDs, before hex rules can shred them.
	{regexp.MustCompile(`\b[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}\b`), "<uuid>"},
	// Email addresses, before the hostname-ish and number rules.
	{regexp.MustCompile(`\b[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}\b`), "<email>"},
	// IPv4, with optional :port.
	{regexp.MustCompile(`\b(?:\d{1,3}\.){3}\d{1,3}(?::\d{1,5})?\b`), "<ip>"},
	// 0x-prefixed hex of any length, then bare hex-alphabet runs of ≥12
	// chars containing a digit (so "9f8e7d6c5b4a" masks but a long
	// all-letter word never does; see maskLongHex below).
	{regexp.MustCompile(`\b0[xX][0-9a-fA-F]+\b`), "<hex>"},
	{regexp.MustCompile(`\b[0-9a-fA-F]{12,}\b`), maskLongHex},
	// Durations and byte sizes: a number glued to a unit.
	{regexp.MustCompile(`\b\d+(?:\.\d+)?(?:ns|us|µs|ms|s|m|h)\b`), "<dur>"},
	{regexp.MustCompile(`(?i)\b\d+(?:\.\d+)?\s?(?:B|KB|KiB|MB|MiB|GB|GiB|TB|TiB)\b`), "<size>"},
	// Double-quoted strings (contents are almost always data).
	{regexp.MustCompile(`"(?:[^"\\]|\\.)*"`), `"<str>"`},
	// Single-quoted strings need boundaries so apostrophes in prose
	// ("can't open 'x'") don't pair up. RE2 has no lookarounds, so the
	// boundaries are captured and re-emitted; the rule appears twice
	// because a consumed trailing boundary can hide an adjacent token.
	{singleQuoteRE, `${1}'<str>'${2}`},
	{singleQuoteRE, `${1}'<str>'${2}`},
	// Bare numbers, last: ints, floats, scientific. \b keeps digits glued
	// inside identifiers ("v2", "sha256") intact.
	{regexp.MustCompile(`\b\d+(?:\.\d+)?(?:[eE][+-]?\d+)?\b`), "<n>"},
	// Boolean flag values ("hit=false" / "hit=true" are one statement).
	{regexp.MustCompile(`=(?:true|false)\b`), "=<bool>"},
}

var singleQuoteRE = regexp.MustCompile(`(^|[\s=:([])'(?:[^'\\]|\\.)*'($|[\s,.;:)\]])`)

// maskLongHex is a sentinel placeholder: the hex-run rule matches any
// 12+ character [0-9a-f] run and masks the ones containing a digit.
// All-letter runs are English words, never IDs. All-digit runs DO mask as
// <hex>: a hex-ID field emits an all-digit value ~0.4% of the time
// ((10/16)^12), and letting those fall through to <n> would split one
// statement into two templates.
const maskLongHex = "\x00longhex"

func looksHex(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= '0' && s[i] <= '9' {
			return true
		}
	}
	return false
}

// pathSegRE finds slash-separated tokens (URL paths, file paths) so their
// variable segments can be masked individually, keeping the route shape:
// /api/items/42/orders → /api/items/<*>/orders.
var pathSegRE = regexp.MustCompile(`(?:^|[\s=])(/[^\s"']+)`)

// segmentIsVariable reports whether one path segment looks like data:
// purely numeric, hex-ish (≥8 chars with a digit), or a UUID remnant.
var segmentNumRE = regexp.MustCompile(`^\d+$`)
var segmentHexRE = regexp.MustCompile(`^[0-9a-fA-F-]{8,}$`)

func segmentIsVariable(seg string) bool {
	if segmentNumRE.MatchString(seg) {
		return true
	}
	if segmentHexRE.MatchString(seg) && strings.ContainsAny(seg, "0123456789") {
		return true
	}
	return false
}

// maskPathSegments rewrites the variable segments of one path token.
func maskPathSegments(path string) string {
	segs := strings.Split(path, "/")
	changed := false
	for i, seg := range segs {
		// Strip a trailing query string before judging the segment.
		base := seg
		if q := strings.IndexByte(base, '?'); q >= 0 {
			base = base[:q]
		}
		if segmentIsVariable(base) {
			segs[i] = "<*>" + seg[len(base):]
			changed = true
		}
	}
	if !changed {
		return path
	}
	return strings.Join(segs, "/")
}

// Mask replaces variable tokens in a log message with typed placeholders.
// The result is the template identity used for grouping. Already-masked
// input passes through unchanged (idempotence).
func Mask(msg string) string {
	if msg == "" {
		return msg
	}
	// Paths first: their numeric segments must be judged in path context,
	// not swallowed whole by the bare-number rule.
	msg = pathSegRE.ReplaceAllStringFunc(msg, func(m string) string {
		lead := ""
		p := m
		if p[0] != '/' {
			lead, p = m[:1], m[1:]
		}
		return lead + maskPathSegments(p)
	})
	for _, r := range rules {
		if r.placeholder == maskLongHex {
			msg = r.re.ReplaceAllStringFunc(msg, func(m string) string {
				if looksHex(m) {
					return "<hex>"
				}
				return m
			})
			continue
		}
		msg = r.re.ReplaceAllString(msg, r.placeholder)
	}
	return msg
}

// Key builds the stable identity of a statement: level + logger + masked
// message + the structured-key shape. Two lines with the same key are, for
// logdiet's purposes, the same log statement.
func Key(level, logger, masked string, keys []string) string {
	var b strings.Builder
	b.WriteString(level)
	b.WriteByte('\x1f')
	b.WriteString(logger)
	b.WriteByte('\x1f')
	b.WriteString(masked)
	b.WriteByte('\x1f')
	b.WriteString(strings.Join(keys, ","))
	return b.String()
}

// ID derives the short stable statement id (s:xxxxxxxx) shown in reports,
// an FNV-1a hash of the full key. Stable across runs and machines.
func ID(key string) string {
	h := fnv.New32a()
	h.Write([]byte(key))
	const hexdigits = "0123456789abcdef"
	sum := h.Sum32()
	var out [8]byte
	for i := 7; i >= 0; i-- {
		out[i] = hexdigits[sum&0xf]
		sum >>= 4
	}
	return "s:" + string(out[:])
}
