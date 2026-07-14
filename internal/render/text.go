package render

import (
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	"github.com/JaydenCJ/logdiet/internal/aggregate"
	"github.com/JaydenCJ/logdiet/internal/cost"
)

// templateWidth caps the statement column so tables stay readable in a
// terminal; the full template is always available via --format json.
const templateWidth = 76

// Text renders the rank report for terminals.
func Text(w io.Writer, ctx Context) {
	rep := ctx.Rep
	fmt.Fprintf(w, "logdiet rank — %s %s, %s across %s\n",
		Count(rep.Lines), plural(rep.Lines, "line", "lines"),
		Bytes(float64(rep.Bytes)), inputsLabel(ctx.Inputs))

	if ws, ok := windowString(rep); ok {
		fmt.Fprintf(w, "window: %s\n", ws)
		fmt.Fprintf(w, "rate:   %s/day  →  est %s/mo at $%.2f/GB ingested\n",
			Bytes(ctx.Model.BytesPerDay), monthlyTotal(ctx), ctx.Model.PricePerGB)
	} else {
		fmt.Fprintf(w, "window: no usable timestamps — showing shares only (no $/day extrapolation)\n")
	}
	fmt.Fprintln(w)

	// Per-level summary.
	fmt.Fprintf(w, "%-9s %10s %12s %8s\n", "by level", "lines", "bytes", "share")
	for _, lvl := range levelOrder {
		lines := rep.LevelLines[lvl]
		if lines == 0 {
			continue
		}
		fmt.Fprintf(w, "  %-7s %10s %12s %7.1f%%\n",
			lvl, Count(lines), Bytes(float64(rep.LevelBytes[lvl])), sharePct(rep.LevelBytes[lvl], rep.Bytes))
	}
	fmt.Fprintln(w)

	// Ranked statements.
	stats := rep.Statements
	shown := len(stats)
	if ctx.Top > 0 && ctx.Top < shown {
		shown = ctx.Top
	}
	byLabel := "bytes"
	if ctx.By == aggregate.ByCount {
		byLabel = "count"
	}
	fmt.Fprintf(w, "top %d of %s %s by %s\n", shown, Count(int64(len(stats))),
		plural(int64(len(stats)), "statement", "statements"), byLabel)
	priced := ctx.Model.HasRate
	if priced {
		fmt.Fprintf(w, "%4s  %-6s %-7s %9s %11s %7s %9s  %s\n",
			"#", "action", "level", "count", "bytes", "share", "$/mo", "statement")
	} else {
		fmt.Fprintf(w, "%4s  %-6s %-7s %9s %11s %7s  %s\n",
			"#", "action", "level", "count", "bytes", "share", "statement")
	}
	for i, st := range stats[:shown] {
		action := cost.Suggest(st, ctx.Keep, rep.Bytes)
		if priced {
			usd, _ := ctx.Model.MonthlyUSD(st.Bytes, rep.Bytes)
			fmt.Fprintf(w, "%4d  %-6s %-7s %9s %11s %6.1f%% %9s  %s\n",
				i+1, action, st.Level, Count(st.Count), Bytes(float64(st.Bytes)),
				sharePct(st.Bytes, rep.Bytes), USD(usd), displayTemplate(st.Logger, st.Template, st.Keys))
		} else {
			fmt.Fprintf(w, "%4d  %-6s %-7s %9s %11s %6.1f%%  %s\n",
				i+1, action, st.Level, Count(st.Count), Bytes(float64(st.Bytes)),
				sharePct(st.Bytes, rep.Bytes), displayTemplate(st.Logger, st.Template, st.Keys))
		}
	}

	if rep.OverflowLines > 0 {
		fmt.Fprintf(w, "\noverflow: pooled %s %s (%s) beyond the %s-statement cap\n",
			Count(rep.OverflowLines), plural(rep.OverflowLines, "line", "lines"),
			Bytes(float64(rep.OverflowBytes)), Count(int64(rep.StatementCap)))
	}

	// Footer: how much demotion alone could cut.
	var demotable int64
	for _, st := range stats {
		if cost.Suggest(st, ctx.Keep, rep.Bytes) == cost.ActionDemote {
			demotable += st.Bytes
		}
	}
	fmt.Fprintf(w, "\ndemotable below %s: %.1f%% of all bytes — `logdiet plan --target N` builds the hit list\n",
		ctx.Keep, sharePct(demotable, rep.Bytes))
}

// PlanText renders the demotion hit list for terminals.
func PlanText(w io.Writer, ctx Context, plan cost.Plan) {
	rep := ctx.Rep
	fmt.Fprintf(w, "logdiet plan — cut %.1f%% of log bytes by demoting statements below %s\n",
		plan.TargetPct, plan.Keep)
	fmt.Fprintf(w, "input: %s %s, %s; demotable ceiling: %.1f%% of bytes\n\n",
		Count(rep.Lines), plural(rep.Lines, "line", "lines"),
		Bytes(float64(rep.Bytes)), plan.DemotablePct)

	if len(plan.Items) == 0 {
		fmt.Fprintf(w, "nothing to demote below %s.\n", plan.Keep)
	}
	for i, item := range plan.Items {
		st := item.Stat
		line := fmt.Sprintf("%4d  %-7s %6.1f%%  cum %5.1f%%  %11s",
			i+1, st.Level, item.SharePct, item.CumPct, Bytes(float64(st.Bytes)))
		if item.HasUSD {
			line += fmt.Sprintf("  %9s/mo", USD(item.MonthlyUSD))
		}
		fmt.Fprintf(w, "%s  %s\n", line, displayTemplate(st.Logger, st.Template, st.Keys))
	}
	fmt.Fprintln(w)

	saved := ""
	if plan.HasUSD {
		saved = fmt.Sprintf(", est %s/mo saved", USD(plan.MonthlyUSD))
	}
	if plan.Achieved {
		fmt.Fprintf(w, "plan: demote %d %s → cut %.1f%% (target %.1f%%)%s\n",
			len(plan.Items), plural(int64(len(plan.Items)), "statement", "statements"),
			plan.AchievedPct, plan.TargetPct, saved)
		fmt.Fprintf(w, "plan: OK\n")
	} else {
		fmt.Fprintf(w, "plan: demoting everything below %s cuts %.1f%% (target %.1f%%)%s\n",
			plan.Keep, plan.AchievedPct, plan.TargetPct, saved)
		fmt.Fprintf(w, "plan: SHORT — the rest needs sampling, trimming, or dropping kept statements\n")
	}
}

// displayTemplate renders the statement identity for humans: optional
// logger prefix, the masked message, and the structured-key shape.
func displayTemplate(logger, tmpl string, keys []string) string {
	var b strings.Builder
	if logger != "" {
		b.WriteString(logger)
		b.WriteString(" — ")
	}
	if tmpl == "" {
		b.WriteString("(no message)")
	} else {
		b.WriteString(tmpl)
	}
	if len(keys) > 0 {
		b.WriteString(" {")
		b.WriteString(strings.Join(keys, ","))
		b.WriteString("}")
	}
	s := b.String()
	if len(s) > templateWidth {
		s = truncateRunes(s, templateWidth)
	}
	return s
}

// truncateRunes cuts at a rune boundary and appends an ellipsis.
func truncateRunes(s string, n int) string {
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n] + "…"
}

func inputsLabel(inputs []string) string {
	switch len(inputs) {
	case 0:
		return "stdin"
	case 1:
		return inputs[0]
	default:
		return fmt.Sprintf("%d files", len(inputs))
	}
}

func monthlyTotal(ctx Context) string {
	usd, ok := ctx.Model.MonthlyUSD(ctx.Rep.Bytes, ctx.Rep.Bytes)
	if !ok {
		return "—"
	}
	return USD(usd)
}
