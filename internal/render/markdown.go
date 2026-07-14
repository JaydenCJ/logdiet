package render

import (
	"fmt"
	"io"
	"strings"

	"github.com/JaydenCJ/logdiet/internal/cost"
)

// Markdown renders the rank report as a PR-ready Markdown document.
func Markdown(w io.Writer, ctx Context) {
	rep := ctx.Rep
	fmt.Fprintf(w, "## logdiet rank\n\n")
	fmt.Fprintf(w, "**%s** %s, **%s** across %s.", Count(rep.Lines),
		plural(rep.Lines, "line", "lines"), Bytes(float64(rep.Bytes)), inputsLabel(ctx.Inputs))
	if ws, ok := windowString(rep); ok {
		fmt.Fprintf(w, " Window %s — **%s/day**, est **%s/mo** at $%.2f/GB.",
			ws, Bytes(ctx.Model.BytesPerDay), monthlyTotal(ctx), ctx.Model.PricePerGB)
	}
	fmt.Fprintf(w, "\n\n")

	fmt.Fprintf(w, "| # | Action | Level | Count | Bytes | Share | Statement |\n")
	fmt.Fprintf(w, "|---|---|---|---:|---:|---:|---|\n")
	stats := rep.Statements
	if ctx.Top > 0 && ctx.Top < len(stats) {
		stats = stats[:ctx.Top]
	}
	for i, st := range stats {
		action := cost.Suggest(st, ctx.Keep, rep.Bytes)
		fmt.Fprintf(w, "| %d | %s | %s | %s | %s | %.1f%% | `%s` |\n",
			i+1, action, st.Level, Count(st.Count), Bytes(float64(st.Bytes)),
			sharePct(st.Bytes, rep.Bytes), mdCode(displayTemplate(st.Logger, st.Template, st.Keys)))
	}

	var demotable int64
	for _, st := range rep.Statements {
		if cost.Suggest(st, ctx.Keep, rep.Bytes) == cost.ActionDemote {
			demotable += st.Bytes
		}
	}
	fmt.Fprintf(w, "\nDemotable below **%s**: **%.1f%%** of all bytes.\n",
		ctx.Keep, sharePct(demotable, rep.Bytes))
}

// mdCode makes a template safe inside a Markdown table code span: pipes
// would split the cell and backticks would close the span.
func mdCode(s string) string {
	s = strings.ReplaceAll(s, "|", "\\|")
	s = strings.ReplaceAll(s, "`", "'")
	return s
}
