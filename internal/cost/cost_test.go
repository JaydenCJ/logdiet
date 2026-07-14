// Pricing and planning tests. The math here is what turns logdiet's byte
// counts into the numbers people put in tickets, so every formula gets an
// exact-value test with hand-computed expectations.
package cost

import (
	"fmt"
	"math"
	"testing"

	"github.com/JaydenCJ/logdiet/internal/aggregate"
	"github.com/JaydenCJ/logdiet/internal/parse"
)

// repFrom builds a report by feeding synthetic lines to the real
// aggregator, so cost tests exercise the same pipeline the CLI uses.
func repFrom(lines []string) *aggregate.Report {
	a := aggregate.New(0)
	for _, l := range lines {
		a.Add(parse.Line(l, parse.Options{}), l)
	}
	return a.Finish(aggregate.ByBytes)
}

// timedLines emits n copies of a JSON statement spread over a window so
// the report gets a usable rate.
func timedLines(level, msg string, n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf(`{"level":%q,"time":"2026-07-01T%02d:00:00Z","msg":%q}`, level, i%24, msg)
	}
	return out
}

func approx(t *testing.T, got, want, tol float64, what string) {
	t.Helper()
	if math.Abs(got-want) > tol {
		t.Fatalf("%s = %g, want %g (±%g)", what, got, want, tol)
	}
}

func TestModelExtrapolatesDailyRate(t *testing.T) {
	// 6-hour window → daily rate is exactly 4× the observed bytes.
	rep := repFrom([]string{
		`{"level":"info","time":"2026-07-01T00:00:00Z","msg":"a"}`,
		`{"level":"info","time":"2026-07-01T06:00:00Z","msg":"b"}`,
	})
	m := NewModel(rep, DefaultPricePerGB)
	if !m.HasRate {
		t.Fatal("expected a rate from a 6h window")
	}
	approx(t, m.BytesPerDay, float64(rep.Bytes)*4, 0.001, "BytesPerDay")
}

func TestModelWithoutWindowHasNoRate(t *testing.T) {
	rep := repFrom([]string{"plain one", "plain two"})
	m := NewModel(rep, DefaultPricePerGB)
	if m.HasRate {
		t.Fatal("rate without timestamps")
	}
	if _, ok := m.MonthlyUSD(100, 200); ok {
		t.Fatal("MonthlyUSD must refuse without a rate")
	}
	if _, ok := m.StatementBytesPerDay(100, 200); ok {
		t.Fatal("StatementBytesPerDay must refuse without a rate")
	}
}

func TestMonthlyUSDExactMath(t *testing.T) {
	// Construct 1 GB/day directly: window 24h, total bytes = 1 GB.
	rep := repFrom([]string{
		`{"level":"info","time":"2026-07-01T00:00:00Z","msg":"a"}`,
		`{"level":"info","time":"2026-07-02T00:00:00Z","msg":"b"}`,
	})
	m := NewModel(rep, 0.50)
	m.BytesPerDay = GB // pin the rate; the window flag stays true
	usd, ok := m.MonthlyUSD(rep.Bytes, rep.Bytes)
	if !ok {
		t.Fatal("expected a priced result")
	}
	// 1 GB/day × 30 days × $0.50/GB = $15.00/mo.
	approx(t, usd, 15.0, 0.0001, "MonthlyUSD")
	// Half the bytes → half the money.
	half, _ := m.MonthlyUSD(rep.Bytes/2, rep.Bytes)
	approx(t, half, 7.5, 0.01, "MonthlyUSD(half)")
}

func TestSuggestDemotesBelowKeep(t *testing.T) {
	st := &aggregate.Stat{Level: parse.Debug, Bytes: 10}
	if got := Suggest(st, parse.Warn, 1000); got != ActionDemote {
		t.Fatalf("debug below warn = %v, want demote", got)
	}
	st.Level = parse.Info
	if got := Suggest(st, parse.Info, 1000); got != ActionKeep {
		t.Fatalf("info at keep=info = %v, want keep (not strictly below)", got)
	}
	// Unknown must never demote: we cannot prove it is safe to drop.
	st.Level = parse.Unknown
	if got := Suggest(st, parse.Warn, 1000); got == ActionDemote {
		t.Fatal("unknown-level statement was marked demotable")
	}
}

func TestSuggestFlagsHeavyKeptStatementsForReview(t *testing.T) {
	st := &aggregate.Stat{Level: parse.Error, Bytes: 150} // 15% of 1000
	if got := Suggest(st, parse.Warn, 1000); got != ActionReview {
		t.Fatalf("heavy error = %v, want review", got)
	}
	st.Bytes = 10 // 1%
	if got := Suggest(st, parse.Warn, 1000); got != ActionKeep {
		t.Fatalf("light error = %v, want keep", got)
	}
}

func TestPlanGreedyStopsAtTarget(t *testing.T) {
	var lines []string
	lines = append(lines, timedLines("debug", "big debug statement with padding padding padding", 50)...)
	lines = append(lines, timedLines("info", "medium info statement", 30)...)
	lines = append(lines, timedLines("info", "tiny", 5)...)
	lines = append(lines, timedLines("error", "kept error", 10)...)
	rep := repFrom(lines)
	m := NewModel(rep, DefaultPricePerGB)

	plan := Build(rep, m, 40, parse.Warn)
	if !plan.Achieved {
		t.Fatalf("plan short: %+v", plan)
	}
	if len(plan.Items) == 0 || plan.Items[0].Stat.Level != parse.Debug {
		t.Fatalf("greedy order wrong: first item %+v", plan.Items[0])
	}
	// Items must be needed: dropping the last one falls below target.
	if len(plan.Items) > 1 {
		prev := plan.Items[len(plan.Items)-2].CumPct
		if prev >= plan.TargetPct {
			t.Fatalf("plan took an unnecessary item: cum %.1f already >= %.1f", prev, plan.TargetPct)
		}
	}
	// Cumulative percentages must be monotonically increasing.
	for i := 1; i < len(plan.Items); i++ {
		if plan.Items[i].CumPct <= plan.Items[i-1].CumPct {
			t.Fatalf("cum not increasing at %d", i)
		}
	}
}

func TestPlanUnreachableTargetReturnsEverything(t *testing.T) {
	lines := append(timedLines("debug", "small debug", 2), timedLines("error", "huge error statement with lots of padding", 50)...)
	rep := repFrom(lines)
	plan := Build(rep, NewModel(rep, DefaultPricePerGB), 90, parse.Warn)
	if plan.Achieved {
		t.Fatal("plan claims an unreachable target")
	}
	if len(plan.Items) != 1 {
		t.Fatalf("expected every demotable statement, got %d", len(plan.Items))
	}
	approx(t, plan.AchievedPct, plan.DemotablePct, 0.001, "achieved == ceiling when short")
}

func TestPlanRespectsKeepLevel(t *testing.T) {
	lines := append(timedLines("debug", "debug statement", 10), timedLines("info", "info statement", 10)...)
	rep := repFrom(lines)
	plan := Build(rep, NewModel(rep, DefaultPricePerGB), 100, parse.Info)
	for _, item := range plan.Items {
		if item.Stat.Level >= parse.Info {
			t.Fatalf("keep=info plan demoted %v", item.Stat.Level)
		}
	}
	if len(plan.Items) != 1 {
		t.Fatalf("items = %d, want only the debug statement", len(plan.Items))
	}
}

func TestPlanEmptyReport(t *testing.T) {
	plan := Build(repFrom(nil), Model{}, 40, parse.Warn)
	if len(plan.Items) != 0 || plan.Achieved {
		t.Fatalf("empty input produced a plan: %+v", plan)
	}
}

func TestPlanSharesSumToCumulative(t *testing.T) {
	var lines []string
	lines = append(lines, timedLines("debug", "one statement here", 20)...)
	lines = append(lines, timedLines("info", "another statement over there", 20)...)
	rep := repFrom(lines)
	plan := Build(rep, NewModel(rep, DefaultPricePerGB), 100, parse.Warn)
	var sum float64
	for _, item := range plan.Items {
		sum += item.SharePct
	}
	approx(t, sum, plan.AchievedPct, 0.01, "sum of shares vs achieved")
}
