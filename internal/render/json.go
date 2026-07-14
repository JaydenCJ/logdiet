package render

import (
	"encoding/json"
	"io"
	"time"

	"github.com/JaydenCJ/logdiet/internal/aggregate"
	"github.com/JaydenCJ/logdiet/internal/cost"
	"github.com/JaydenCJ/logdiet/internal/version"
)

// SchemaVersion identifies the JSON envelope shape; it bumps on any
// backwards-incompatible field change so scripts can pin what they parse.
const SchemaVersion = 1

type jsonEnvelope struct {
	Tool          string           `json:"tool"`
	Version       string           `json:"version"`
	SchemaVersion int              `json:"schema_version"`
	Command       string           `json:"command"`
	Inputs        []string         `json:"inputs"`
	Totals        jsonTotals       `json:"totals"`
	Window        *jsonWindow      `json:"window"`
	Rate          *jsonRate        `json:"rate"`
	Levels        []jsonLevel      `json:"levels"`
	Formats       map[string]int64 `json:"formats"`
	Keep          string           `json:"keep"`
	Statements    []jsonStatement  `json:"statements,omitempty"`
	Plan          *jsonPlan        `json:"plan,omitempty"`
	Overflow      *jsonOverflow    `json:"overflow,omitempty"`
}

type jsonTotals struct {
	Lines      int64 `json:"lines"`
	Bytes      int64 `json:"bytes"`
	Statements int   `json:"statements"`
}

type jsonWindow struct {
	First   string  `json:"first"`
	Last    string  `json:"last"`
	Seconds float64 `json:"seconds"`
}

type jsonRate struct {
	BytesPerDay float64 `json:"bytes_per_day"`
	PricePerGB  float64 `json:"price_per_gb"`
	MonthlyUSD  float64 `json:"monthly_usd"`
}

type jsonLevel struct {
	Level    string  `json:"level"`
	Lines    int64   `json:"lines"`
	Bytes    int64   `json:"bytes"`
	SharePct float64 `json:"share_pct"`
}

type jsonStatement struct {
	ID         string   `json:"id"`
	Level      string   `json:"level"`
	Logger     string   `json:"logger,omitempty"`
	Template   string   `json:"template"`
	Keys       []string `json:"keys,omitempty"`
	Count      int64    `json:"count"`
	Bytes      int64    `json:"bytes"`
	SharePct   float64  `json:"share_pct"`
	Action     string   `json:"action"`
	MonthlyUSD *float64 `json:"monthly_usd,omitempty"`
	Sample     string   `json:"sample"`
}

type jsonPlan struct {
	TargetPct    float64        `json:"target_pct"`
	AchievedPct  float64        `json:"achieved_pct"`
	DemotablePct float64        `json:"demotable_pct"`
	Achieved     bool           `json:"achieved"`
	MonthlyUSD   *float64       `json:"monthly_usd,omitempty"`
	Items        []jsonPlanItem `json:"items"`
}

type jsonPlanItem struct {
	ID         string   `json:"id"`
	Level      string   `json:"level"`
	Template   string   `json:"template"`
	Bytes      int64    `json:"bytes"`
	SharePct   float64  `json:"share_pct"`
	CumPct     float64  `json:"cum_pct"`
	MonthlyUSD *float64 `json:"monthly_usd,omitempty"`
}

type jsonOverflow struct {
	StatementCap int   `json:"statement_cap"`
	Lines        int64 `json:"lines"`
	Bytes        int64 `json:"bytes"`
}

// round2 keeps floats in the JSON stable and readable (2 decimals is the
// precision the text renderer promises anyway).
func round2(v float64) float64 {
	return float64(int64(v*100+0.5)) / 100
}

func buildEnvelope(command string, ctx Context) jsonEnvelope {
	rep := ctx.Rep
	env := jsonEnvelope{
		Tool:          "logdiet",
		Version:       version.Version,
		SchemaVersion: SchemaVersion,
		Command:       command,
		Inputs:        ctx.Inputs,
		Totals:        jsonTotals{Lines: rep.Lines, Bytes: rep.Bytes, Statements: len(rep.Statements)},
		Formats:       rep.Formats,
		Keep:          ctx.Keep.String(),
	}
	if env.Inputs == nil {
		env.Inputs = []string{"stdin"}
	}
	if w, ok := rep.Window(); ok {
		env.Window = &jsonWindow{
			First:   rep.First.UTC().Format(time.RFC3339),
			Last:    rep.Last.UTC().Format(time.RFC3339),
			Seconds: w.Seconds(),
		}
	}
	if ctx.Model.HasRate {
		total, _ := ctx.Model.MonthlyUSD(rep.Bytes, rep.Bytes)
		env.Rate = &jsonRate{
			BytesPerDay: round2(ctx.Model.BytesPerDay),
			PricePerGB:  ctx.Model.PricePerGB,
			MonthlyUSD:  round2(total),
		}
	}
	for _, lvl := range levelOrder {
		if rep.LevelLines[lvl] == 0 {
			continue
		}
		env.Levels = append(env.Levels, jsonLevel{
			Level:    lvl,
			Lines:    rep.LevelLines[lvl],
			Bytes:    rep.LevelBytes[lvl],
			SharePct: round2(sharePct(rep.LevelBytes[lvl], rep.Bytes)),
		})
	}
	if rep.OverflowLines > 0 {
		env.Overflow = &jsonOverflow{
			StatementCap: rep.StatementCap,
			Lines:        rep.OverflowLines,
			Bytes:        rep.OverflowBytes,
		}
	}
	return env
}

func statementJSON(st *aggregate.Stat, ctx Context) jsonStatement {
	js := jsonStatement{
		ID:       st.ID,
		Level:    st.Level.String(),
		Logger:   st.Logger,
		Template: st.Template,
		Keys:     st.Keys,
		Count:    st.Count,
		Bytes:    st.Bytes,
		SharePct: round2(sharePct(st.Bytes, ctx.Rep.Bytes)),
		Action:   string(cost.Suggest(st, ctx.Keep, ctx.Rep.Bytes)),
		Sample:   st.Sample,
	}
	if usd, ok := ctx.Model.MonthlyUSD(st.Bytes, ctx.Rep.Bytes); ok {
		v := round2(usd)
		js.MonthlyUSD = &v
	}
	return js
}

// JSON renders the rank report as an indented, stable JSON document.
func JSON(w io.Writer, ctx Context) error {
	env := buildEnvelope("rank", ctx)
	stats := ctx.Rep.Statements
	if ctx.Top > 0 && ctx.Top < len(stats) {
		stats = stats[:ctx.Top]
	}
	for _, st := range stats {
		env.Statements = append(env.Statements, statementJSON(st, ctx))
	}
	return writeJSON(w, env)
}

// PlanJSON renders the demotion plan as JSON.
func PlanJSON(w io.Writer, ctx Context, plan cost.Plan) error {
	env := buildEnvelope("plan", ctx)
	jp := &jsonPlan{
		TargetPct:    plan.TargetPct,
		AchievedPct:  round2(plan.AchievedPct),
		DemotablePct: round2(plan.DemotablePct),
		Achieved:     plan.Achieved,
		Items:        []jsonPlanItem{},
	}
	if plan.HasUSD {
		v := round2(plan.MonthlyUSD)
		jp.MonthlyUSD = &v
	}
	for _, item := range plan.Items {
		ji := jsonPlanItem{
			ID:       item.Stat.ID,
			Level:    item.Stat.Level.String(),
			Template: item.Stat.Template,
			Bytes:    item.Stat.Bytes,
			SharePct: round2(item.SharePct),
			CumPct:   round2(item.CumPct),
		}
		if item.HasUSD {
			v := round2(item.MonthlyUSD)
			ji.MonthlyUSD = &v
		}
		jp.Items = append(jp.Items, ji)
	}
	env.Plan = jp
	return writeJSON(w, env)
}

func writeJSON(w io.Writer, env jsonEnvelope) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(env)
}
