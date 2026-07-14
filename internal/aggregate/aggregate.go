// Package aggregate folds parsed log records into per-statement totals.
// It is the bookkeeping layer between parsing and pricing: every line's
// bytes land either on a statement bucket or on the bounded overflow
// bucket, so the report's totals always equal the input exactly.
package aggregate

import (
	"sort"
	"time"
	"unicode/utf8"

	"github.com/JaydenCJ/logdiet/internal/parse"
	"github.com/JaydenCJ/logdiet/internal/template"
)

// Stat is the accumulated footprint of one log statement.
type Stat struct {
	ID       string      // stable short id, e.g. s:1a2b3c4d
	Level    parse.Level // normalized severity
	Logger   string      // optional logger/component
	Template string      // masked message (the statement identity)
	Keys     []string    // structured-key shape, sorted
	Count    int64       // lines attributed to this statement
	Bytes    int64       // raw bytes attributed (incl. newline)
	Sample   string      // first raw line seen, truncated for display
}

// Report is the finished aggregation: sorted statements plus totals.
type Report struct {
	Statements []*Stat
	Lines      int64
	Bytes      int64

	// Per-level and per-format tallies (indexed by String() name).
	LevelLines map[string]int64
	LevelBytes map[string]int64
	Formats    map[string]int64

	// Observed time window, when any line carried a parseable timestamp.
	First, Last time.Time
	HasWindow   bool

	// Overflow accounting when the statement cap was hit: lines/bytes
	// whose template was not yet tracked land in one pooled bucket, so
	// totals still equal the input. Zero unless the cap triggered.
	// StatementCap echoes the configured cap for honest reporting.
	StatementCap  int
	OverflowLines int64
	OverflowBytes int64
}

// sampleLimit caps stored sample lines so a single 2 MB stack-trace line
// cannot bloat the report.
const sampleLimit = 400

// DefaultMaxStatements bounds the number of distinct templates held in
// memory. 100k templates × ~a few hundred bytes is comfortably small, and
// any healthy codebase has far fewer distinct statements than that.
const DefaultMaxStatements = 100_000

// Aggregator accumulates records. Not safe for concurrent use; the CLI
// reads files sequentially, which is I/O-bound anyway.
type Aggregator struct {
	max   int
	stats map[string]*Stat
	rep   Report
}

// New returns an Aggregator. maxStatements ≤ 0 selects the default cap.
func New(maxStatements int) *Aggregator {
	if maxStatements <= 0 {
		maxStatements = DefaultMaxStatements
	}
	return &Aggregator{
		max:   maxStatements,
		stats: make(map[string]*Stat),
		rep: Report{
			StatementCap: maxStatements,
			LevelLines:   map[string]int64{},
			LevelBytes:   map[string]int64{},
			Formats:      map[string]int64{},
		},
	}
}

// Add folds one parsed record (with its raw line, for the sample) into
// the aggregation.
func (a *Aggregator) Add(rec parse.Record, raw string) {
	a.rep.Lines++
	a.rep.Bytes += int64(rec.Bytes)
	lvl := rec.Level.String()
	a.rep.LevelLines[lvl]++
	a.rep.LevelBytes[lvl] += int64(rec.Bytes)
	a.rep.Formats[rec.Format.String()]++
	if rec.HasTime {
		if !a.rep.HasWindow || rec.Time.Before(a.rep.First) {
			a.rep.First = rec.Time
		}
		if !a.rep.HasWindow || rec.Time.After(a.rep.Last) {
			a.rep.Last = rec.Time
		}
		a.rep.HasWindow = true
	}

	masked := template.Mask(rec.Message)
	key := template.Key(lvl, rec.Logger, masked, rec.Keys)
	st, ok := a.stats[key]
	if !ok {
		if len(a.stats) >= a.max {
			a.rep.OverflowLines++
			a.rep.OverflowBytes += int64(rec.Bytes)
			return
		}
		st = &Stat{
			ID:       template.ID(key),
			Level:    rec.Level,
			Logger:   rec.Logger,
			Template: masked,
			Keys:     rec.Keys,
			Sample:   truncate(raw, sampleLimit),
		}
		a.stats[key] = st
	}
	st.Count++
	st.Bytes += int64(rec.Bytes)
}

// SortKey selects the ranking order.
type SortKey int

const (
	ByBytes SortKey = iota
	ByCount
)

// Finish freezes the aggregation into a Report sorted by the given key,
// descending, with a deterministic template tie-break so identical inputs
// always render identical reports.
func (a *Aggregator) Finish(by SortKey) *Report {
	rep := a.rep
	rep.Statements = make([]*Stat, 0, len(a.stats))
	for _, st := range a.stats {
		rep.Statements = append(rep.Statements, st)
	}
	sort.Slice(rep.Statements, func(i, j int) bool {
		x, y := rep.Statements[i], rep.Statements[j]
		var xv, yv int64
		if by == ByCount {
			xv, yv = x.Count, y.Count
		} else {
			xv, yv = x.Bytes, y.Bytes
		}
		if xv != yv {
			return xv > yv
		}
		if x.Template != y.Template {
			return x.Template < y.Template
		}
		return x.ID < y.ID
	})
	return &rep
}

// Window returns the observed span. Spans shorter than a minute are
// reported as absent: extrapolating a day rate from seconds of logs
// produces numbers too silly to print.
func (r *Report) Window() (time.Duration, bool) {
	if !r.HasWindow {
		return 0, false
	}
	d := r.Last.Sub(r.First)
	if d < time.Minute {
		return 0, false
	}
	return d, true
}

// truncate cuts s to at most n bytes without splitting a UTF-8 rune: the
// cut point backs up until the byte after it starts a rune.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n] + "…"
}
