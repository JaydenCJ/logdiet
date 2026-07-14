// Package parse turns raw log lines into normalized records: severity,
// message, structured-key shape, and timestamp. It auto-detects the three
// encodings that dominate real production logs — JSON objects, logfmt
// key=value pairs, and prefixed plain text — line by line, so mixed
// streams (e.g. an app that switched loggers mid-quarter) still work.
package parse

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Format identifies the encoding a line was parsed as.
type Format int

const (
	FormatText Format = iota
	FormatJSON
	FormatLogfmt
)

// String returns the lowercase format name used in JSON output.
func (f Format) String() string {
	switch f {
	case FormatJSON:
		return "json"
	case FormatLogfmt:
		return "logfmt"
	default:
		return "text"
	}
}

// Record is one parsed log line, ready for templating and aggregation.
type Record struct {
	Level   Level
	Message string   // the human message; templating masks its variables
	Logger  string   // optional logger/component name (Java-style text logs)
	Keys    []string // sorted structured keys, excluding level/msg/time keys
	Time    time.Time
	HasTime bool
	Bytes   int // raw line length + 1 for the newline it occupied
	Format  Format
}

// Options controls which field names carry the level, message, and
// timestamp in structured (JSON / logfmt) lines. Extra* names are checked
// before the defaults so user-supplied keys win.
type Options struct {
	ExtraLevelKeys []string
	ExtraMsgKeys   []string
	ExtraTimeKeys  []string
}

// Default field names, in priority order, covering zap, zerolog, slog,
// logrus, pino/bunyan, and the ELK/OTel conventions.
var (
	defaultLevelKeys = []string{"level", "lvl", "severity", "log.level", "loglevel", "severity_text"}
	defaultMsgKeys   = []string{"msg", "message", "event", "body", "text"}
	defaultTimeKeys  = []string{"time", "ts", "timestamp", "@timestamp", "datetime", "t"}
)

func (o Options) levelKeys() []string { return append(o.ExtraLevelKeys, defaultLevelKeys...) }
func (o Options) msgKeys() []string   { return append(o.ExtraMsgKeys, defaultMsgKeys...) }
func (o Options) timeKeys() []string  { return append(o.ExtraTimeKeys, defaultTimeKeys...) }

// logfmtStartRE decides whether a non-JSON line is logfmt: it must contain
// a bare key=… token (identifier characters only) at a word boundary.
var logfmtStartRE = regexp.MustCompile(`(?:^|\s)[A-Za-z_][A-Za-z0-9_.@/-]*=`)

// Line parses one raw log line. It never fails: unparseable lines become
// plain-text records with Unknown level, so every byte in the input is
// accounted for in the totals.
func Line(raw string, opts Options) Record {
	rec := Record{Bytes: len(raw) + 1, Format: FormatText}
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return rec
	}
	if strings.HasPrefix(trimmed, "{") && strings.HasSuffix(trimmed, "}") {
		if r, ok := parseJSON(trimmed, opts); ok {
			r.Bytes = rec.Bytes
			return r
		}
	}
	if logfmtStartRE.MatchString(trimmed) {
		if r, ok := parseLogfmt(trimmed, opts); ok {
			r.Bytes = rec.Bytes
			return r
		}
	}
	r := parseText(trimmed)
	r.Bytes = rec.Bytes
	return r
}

// --- JSON ---

func parseJSON(line string, opts Options) (Record, bool) {
	var obj map[string]any
	if err := json.Unmarshal([]byte(line), &obj); err != nil {
		return Record{}, false
	}
	rec := Record{Format: FormatJSON, Level: Unknown}
	used := map[string]bool{}
	// markUsed hides a consumed key from the key-shape; for dotted keys
	// like "log.level" the top-level container is what disappears.
	markUsed := func(k string) {
		head, _, _ := strings.Cut(k, ".")
		used[head] = true
	}

	for _, k := range opts.levelKeys() {
		if v, ok := lookup(obj, k); ok {
			if l := jsonLevel(v); l != Unknown {
				rec.Level = l
				markUsed(k)
				break
			}
		}
	}
	for _, k := range opts.msgKeys() {
		if v, ok := lookup(obj, k); ok {
			if s, ok := v.(string); ok {
				rec.Message = s
				markUsed(k)
				break
			}
		}
	}
	for _, k := range opts.timeKeys() {
		if v, ok := lookup(obj, k); ok {
			if t, ok := jsonTime(v); ok {
				rec.Time, rec.HasTime = t, true
				markUsed(k)
				break
			}
		}
	}
	for k := range obj {
		if !used[k] {
			rec.Keys = append(rec.Keys, k)
		}
	}
	sort.Strings(rec.Keys)
	return rec, true
}

// lookup fetches a possibly-dotted key ("log.level") from a JSON object,
// trying the literal key first, then one level of nesting.
func lookup(obj map[string]any, key string) (any, bool) {
	if v, ok := obj[key]; ok {
		return v, true
	}
	if head, tail, found := strings.Cut(key, "."); found {
		if inner, ok := obj[head].(map[string]any); ok {
			if v, ok := inner[tail]; ok {
				return v, true
			}
		}
	}
	return nil, false
}

func jsonLevel(v any) Level {
	switch x := v.(type) {
	case string:
		return ParseLevel(x)
	case float64:
		// pino/bunyan numeric levels arrive as JSON numbers.
		return ParseLevel(fmt.Sprintf("%.0f", x))
	default:
		return Unknown
	}
}

func jsonTime(v any) (time.Time, bool) {
	switch x := v.(type) {
	case string:
		return parseTimeString(x)
	case float64:
		return parseEpoch(x)
	default:
		return time.Time{}, false
	}
}

// --- logfmt ---

// parseLogfmt scans key=value pairs; values may be double-quoted with \"
// escapes. Bare words between pairs are gathered into the message when no
// msg key is present, so lines like `starting server addr=…` still get a
// meaningful template.
func parseLogfmt(line string, opts Options) (Record, bool) {
	pairs := map[string]string{}
	var order []string
	var bare []string

	i := 0
	for i < len(line) {
		for i < len(line) && line[i] == ' ' {
			i++
		}
		if i >= len(line) {
			break
		}
		start := i
		for i < len(line) && line[i] != ' ' && line[i] != '=' {
			i++
		}
		if i < len(line) && line[i] == '=' && i > start {
			key := line[start:i]
			i++ // consume '='
			var val string
			if i < len(line) && line[i] == '"' {
				i++
				var b strings.Builder
				for i < len(line) && line[i] != '"' {
					if line[i] == '\\' && i+1 < len(line) {
						i++
					}
					b.WriteByte(line[i])
					i++
				}
				i++ // consume closing quote (or run off the end; tolerated)
				val = b.String()
			} else {
				vstart := i
				for i < len(line) && line[i] != ' ' {
					i++
				}
				val = line[vstart:i]
			}
			if _, seen := pairs[key]; !seen {
				order = append(order, key)
			}
			pairs[key] = val
		} else {
			// A bare word (no '='): part of an unkeyed message.
			for i < len(line) && line[i] != ' ' {
				i++
			}
			bare = append(bare, line[start:i])
		}
	}
	if len(pairs) == 0 {
		return Record{}, false
	}

	rec := Record{Format: FormatLogfmt, Level: Unknown}
	used := map[string]bool{}
	for _, k := range opts.levelKeys() {
		if v, ok := pairs[k]; ok {
			if l := ParseLevel(v); l != Unknown {
				rec.Level = l
				used[k] = true
				break
			}
		}
	}
	for _, k := range opts.msgKeys() {
		if v, ok := pairs[k]; ok {
			rec.Message = v
			used[k] = true
			break
		}
	}
	for _, k := range opts.timeKeys() {
		if v, ok := pairs[k]; ok {
			if t, ok := parseTimeString(v); ok {
				rec.Time, rec.HasTime = t, true
				used[k] = true
				break
			}
		}
	}
	if rec.Message == "" && len(bare) > 0 {
		rec.Message = strings.Join(bare, " ")
	}
	for _, k := range order {
		if !used[k] {
			rec.Keys = append(rec.Keys, k)
		}
	}
	sort.Strings(rec.Keys)
	return rec, true
}

// --- plain text ---

// levelTokenRE matches a leading severity token in text logs: `INFO`,
// `[warn]`, `ERROR:`, etc. Case-insensitive; the token must be a known
// level name so ordinary words are not eaten.
var levelTokenRE = regexp.MustCompile(`^\[?([A-Za-z]+)\]?:?\s+`)

// loggerRE matches the Java/Python convention `com.example.Service - msg`
// or `myapp.worker: msg` after the level token: a dotted identifier
// followed by a separator.
var loggerRE = regexp.MustCompile(`^([A-Za-z_][\w$]*(?:\.[\w$]+)+)\s*[-:]\s+`)

func parseText(line string) Record {
	rec := Record{Format: FormatText, Level: Unknown}
	rest, t, ok := stripLeadingTime(line)
	if ok {
		rec.Time, rec.HasTime = t, true
	}
	if m := levelTokenRE.FindStringSubmatch(rest); m != nil {
		if l := ParseLevel(m[1]); l != Unknown {
			rec.Level = l
			rest = rest[len(m[0]):]
		}
	}
	if m := loggerRE.FindStringSubmatch(rest); m != nil {
		rec.Logger = m[1]
		rest = rest[len(m[0]):]
	}
	rec.Message = strings.TrimSpace(rest)
	return rec
}
