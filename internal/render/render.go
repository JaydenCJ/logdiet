// Package render turns a finished aggregation into terminal text, stable
// JSON, or Markdown. Renderers are pure functions of their inputs —
// identical reports produce byte-identical output — which is what makes
// the CLI tests and the smoke script possible.
package render

import (
	"time"

	"github.com/JaydenCJ/logdiet/internal/aggregate"
	"github.com/JaydenCJ/logdiet/internal/cost"
	"github.com/JaydenCJ/logdiet/internal/parse"
)

// Context bundles everything a renderer needs about one analysis run.
type Context struct {
	Rep    *aggregate.Report
	Model  cost.Model
	Keep   parse.Level
	Top    int               // rank: number of statements to show; <=0 means all
	By     aggregate.SortKey // rank: the key the statements are sorted by
	Inputs []string          // display names of the inputs (file paths or "stdin")
}

// plural picks the grammatical form for a count ("1 line", "2 lines").
func plural(n int64, singular, pluralForm string) string {
	if n == 1 {
		return singular
	}
	return pluralForm
}

// levelOrder fixes the row order of per-level tables: severities first,
// most severe last (mirroring how logs escalate), unknown at the end.
var levelOrder = []string{"trace", "debug", "info", "warn", "error", "fatal", "unknown"}

// sharePct is the safe percentage helper used across renderers.
func sharePct(part, total int64) float64 {
	if total == 0 {
		return 0
	}
	return float64(part) / float64(total) * 100
}

// windowString formats the observed window for headers.
func windowString(rep *aggregate.Report) (string, bool) {
	w, ok := rep.Window()
	if !ok {
		return "", false
	}
	return rep.First.UTC().Format(time.RFC3339) + " → " +
		rep.Last.UTC().Format(time.RFC3339) + " (" + w.Truncate(time.Second).String() + ")", true
}
