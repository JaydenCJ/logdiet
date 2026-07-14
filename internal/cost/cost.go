// Package cost turns byte counts into money and demotion advice. It owns
// the three judgment calls the rest of logdiet stays neutral on: how to
// extrapolate a sample window to a daily rate, what a GB of ingest costs,
// and which statements are safe to demote below the production level.
package cost

import (
	"sort"
	"time"

	"github.com/JaydenCJ/logdiet/internal/aggregate"
	"github.com/JaydenCJ/logdiet/internal/parse"
)

// DefaultPricePerGB is the ingest price assumed when --price is not set:
// USD per GB per month, a mid-range figure across hosted log vendors
// (typical list prices run roughly $0.10–$2.50/GB). Always shown in the
// output so nobody mistakes the estimate for a quote.
const DefaultPricePerGB = 0.50

// GB is the decimal gigabyte vendors bill in.
const GB = 1_000_000_000

// Model prices a report: extrapolated daily rate plus monthly dollars.
type Model struct {
	PricePerGB float64

	// Extrapolation, valid when HasRate.
	HasRate     bool
	Window      time.Duration
	BytesPerDay float64
}

// NewModel builds the pricing model for a report. When the report has no
// usable time window, rates and dollar figures are unavailable and every
// consumer must degrade to plain byte shares.
func NewModel(rep *aggregate.Report, pricePerGB float64) Model {
	m := Model{PricePerGB: pricePerGB}
	if w, ok := rep.Window(); ok {
		m.HasRate = true
		m.Window = w
		m.BytesPerDay = float64(rep.Bytes) * float64(24*time.Hour) / float64(w)
	}
	return m
}

// MonthlyUSD prices a byte count that recurs at the report's rate:
// the statement's share of the daily volume, over a 30-day month.
func (m Model) MonthlyUSD(bytes, totalBytes int64) (float64, bool) {
	if !m.HasRate || totalBytes == 0 {
		return 0, false
	}
	share := float64(bytes) / float64(totalBytes)
	return share * m.BytesPerDay * 30 / GB * m.PricePerGB, true
}

// StatementBytesPerDay extrapolates one statement's daily byte rate.
func (m Model) StatementBytesPerDay(bytes, totalBytes int64) (float64, bool) {
	if !m.HasRate || totalBytes == 0 {
		return 0, false
	}
	return float64(bytes) / float64(totalBytes) * m.BytesPerDay, true
}

// Action is the per-statement suggestion shown in the rank table.
type Action string

const (
	// ActionDemote — the statement sits below the keep level; dropping it
	// from production ingest is a pure configuration change.
	ActionDemote Action = "demote"
	// ActionReview — at or above the keep level but a heavy hitter;
	// worth a human look (sampling, rate limits, shorter payloads).
	ActionReview Action = "review"
	// ActionKeep — everything else.
	ActionKeep Action = "keep"
)

// reviewSharePct is the byte share (percent) past which a kept statement
// is flagged for review anyway. One statement eating a tenth of the bill
// deserves attention regardless of its level.
const reviewSharePct = 10.0

// Suggest classifies one statement. keep is the lowest level that must
// survive in production; Unknown-level statements are never demotable
// because we cannot prove what they are.
func Suggest(st *aggregate.Stat, keep parse.Level, totalBytes int64) Action {
	if st.Level != parse.Unknown && st.Level < keep {
		return ActionDemote
	}
	if totalBytes > 0 && float64(st.Bytes)/float64(totalBytes)*100 >= reviewSharePct {
		return ActionReview
	}
	return ActionKeep
}

// PlanItem is one entry in the demotion hit list.
type PlanItem struct {
	Stat       *aggregate.Stat
	SharePct   float64 // this statement's share of total bytes
	CumPct     float64 // running total after taking this statement
	MonthlyUSD float64 // 0 when the model has no rate
	HasUSD     bool
}

// Plan is the answer to "cut our log volume N% — which statements?".
type Plan struct {
	TargetPct    float64
	Keep         parse.Level
	Items        []PlanItem
	AchievedPct  float64 // share reached by demoting every item
	DemotablePct float64 // ceiling: share of all demotable bytes
	Achieved     bool    // AchievedPct >= TargetPct
	MonthlyUSD   float64 // total monthly savings, when priced
	HasUSD       bool
}

// Build computes the smallest greedy hit list reaching targetPct: demotable
// statements (level strictly below keep, never Unknown) sorted by bytes
// descending, taken until the cumulative share covers the target. When the
// target is out of reach it returns every demotable statement and
// Achieved=false, so the caller can see exactly how far demotion alone goes.
func Build(rep *aggregate.Report, m Model, targetPct float64, keep parse.Level) Plan {
	plan := Plan{TargetPct: targetPct, Keep: keep}
	if rep.Bytes == 0 {
		return plan
	}

	var demotable []*aggregate.Stat
	var demotableBytes int64
	for _, st := range rep.Statements {
		if st.Level != parse.Unknown && st.Level < keep {
			demotable = append(demotable, st)
			demotableBytes += st.Bytes
		}
	}
	plan.DemotablePct = float64(demotableBytes) / float64(rep.Bytes) * 100

	// rep.Statements may be count-sorted; the plan always spends bytes.
	sort.Slice(demotable, func(i, j int) bool {
		if demotable[i].Bytes != demotable[j].Bytes {
			return demotable[i].Bytes > demotable[j].Bytes
		}
		return demotable[i].Template < demotable[j].Template
	})

	var cum int64
	for _, st := range demotable {
		if plan.AchievedPct >= targetPct {
			break
		}
		cum += st.Bytes
		item := PlanItem{
			Stat:     st,
			SharePct: float64(st.Bytes) / float64(rep.Bytes) * 100,
			CumPct:   float64(cum) / float64(rep.Bytes) * 100,
		}
		if usd, ok := m.MonthlyUSD(st.Bytes, rep.Bytes); ok {
			item.MonthlyUSD, item.HasUSD = usd, true
		}
		plan.Items = append(plan.Items, item)
		plan.AchievedPct = item.CumPct
	}
	plan.Achieved = plan.AchievedPct >= targetPct
	if usd, ok := m.MonthlyUSD(cum, rep.Bytes); ok {
		plan.MonthlyUSD, plan.HasUSD = usd, true
	}
	return plan
}
