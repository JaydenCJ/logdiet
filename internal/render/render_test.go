// Renderer tests: humanization helpers get exact-value checks, and each
// output format is asserted against the real pipeline's output. Renderers
// must be pure — the determinism test renders twice and compares bytes.
package render

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/JaydenCJ/logdiet/internal/aggregate"
	"github.com/JaydenCJ/logdiet/internal/cost"
	"github.com/JaydenCJ/logdiet/internal/parse"
)

func ctxFrom(lines []string) Context {
	a := aggregate.New(0)
	for _, l := range lines {
		a.Add(parse.Line(l, parse.Options{}), l)
	}
	rep := a.Finish(aggregate.ByBytes)
	return Context{
		Rep:    rep,
		Model:  cost.NewModel(rep, cost.DefaultPricePerGB),
		Keep:   parse.Warn,
		Top:    20,
		Inputs: []string{"app.log"},
	}
}

var timedFixture = []string{
	`{"level":"debug","time":"2026-07-01T00:00:00Z","msg":"cache miss for key 12345","shard":1}`,
	`{"level":"debug","time":"2026-07-01T06:00:00Z","msg":"cache miss for key 98765","shard":2}`,
	`{"level":"info","time":"2026-07-01T12:00:00Z","msg":"request served in 30ms"}`,
	`{"level":"error","time":"2026-07-01T18:00:00Z","msg":"upstream timed out"}`,
}

func TestBytesHumanized(t *testing.T) {
	cases := map[float64]string{
		10:                       "10 B",
		1024:                     "1.0 KiB",
		1536:                     "1.5 KiB",
		5 * 1024 * 1024:          "5.0 MiB",
		2.5 * 1024 * 1024 * 1024: "2.5 GiB",
	}
	for in, want := range cases {
		if got := Bytes(in); got != want {
			t.Errorf("Bytes(%g) = %q, want %q", in, got, want)
		}
	}
}

func TestCountAndUSDFormatting(t *testing.T) {
	counts := map[int64]string{0: "0", 999: "999", 1000: "1,000", 1234567: "1,234,567", -4200: "-4,200"}
	for in, want := range counts {
		if got := Count(in); got != want {
			t.Errorf("Count(%d) = %q, want %q", in, got, want)
		}
	}
	// Cents below $100, whole separated dollars above.
	if got := USD(0.074); got != "$0.07" {
		t.Errorf("USD(0.074) = %q", got)
	}
	if got := USD(1234.6); got != "$1,235" {
		t.Errorf("USD(1234.6) = %q", got)
	}
}

func TestTextReportShape(t *testing.T) {
	var buf bytes.Buffer
	Text(&buf, ctxFrom(timedFixture))
	out := buf.String()
	for _, want := range []string{
		"logdiet rank — 4 lines",
		"window: 2026-07-01T00:00:00Z → 2026-07-01T18:00:00Z (18h0m0s)",
		"by level",
		"cache miss for key <n> {shard}",
		"demote",
		"demotable below warn:",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("text output missing %q\n%s", want, out)
		}
	}
}

func TestTextWithoutTimestampsDegrades(t *testing.T) {
	var buf bytes.Buffer
	Text(&buf, ctxFrom([]string{"plain one", "plain two"}))
	out := buf.String()
	if !strings.Contains(out, "no usable timestamps") {
		t.Fatalf("missing degradation notice:\n%s", out)
	}
	if strings.Contains(out, "$/mo") {
		t.Fatalf("dollar column printed without a rate:\n%s", out)
	}
}

func TestJSONEnvelope(t *testing.T) {
	var buf bytes.Buffer
	if err := JSON(&buf, ctxFrom(timedFixture)); err != nil {
		t.Fatal(err)
	}
	var env map[string]any
	if err := json.Unmarshal(buf.Bytes(), &env); err != nil {
		t.Fatalf("output is not JSON: %v", err)
	}
	if env["tool"] != "logdiet" || env["schema_version"] != float64(1) || env["command"] != "rank" {
		t.Fatalf("envelope = %v", env)
	}
	stmts := env["statements"].([]any)
	if len(stmts) != 3 {
		t.Fatalf("statements = %d, want 3", len(stmts))
	}
	first := stmts[0].(map[string]any)
	for _, field := range []string{"id", "level", "template", "count", "bytes", "share_pct", "action", "sample"} {
		if _, ok := first[field]; !ok {
			t.Errorf("statement missing %q", field)
		}
	}
	if env["window"] == nil || env["rate"] == nil {
		t.Fatal("window/rate absent despite timestamps")
	}
}

func TestJSONTopLimitsStatements(t *testing.T) {
	ctx := ctxFrom(timedFixture)
	ctx.Top = 1
	var buf bytes.Buffer
	if err := JSON(&buf, ctx); err != nil {
		t.Fatal(err)
	}
	var env struct {
		Statements []any `json:"statements"`
		Totals     struct {
			Statements int `json:"statements"`
		} `json:"totals"`
	}
	if err := json.Unmarshal(buf.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	if len(env.Statements) != 1 || env.Totals.Statements != 3 {
		t.Fatalf("top=1: listed %d, totals say %d", len(env.Statements), env.Totals.Statements)
	}
}

func TestMarkdownEscapesTableBreakers(t *testing.T) {
	ctx := ctxFrom([]string{`{"level":"info","msg":"pipe | and backtick ` + "`" + ` inside"}`})
	var buf bytes.Buffer
	Markdown(&buf, ctx)
	out := buf.String()
	if !strings.Contains(out, `\|`) {
		t.Fatalf("unescaped pipe would break the table:\n%s", out)
	}
	// Backticks inside a template must not close the code span.
	if !strings.Contains(out, "backtick ' inside") {
		t.Fatalf("backtick not neutralized:\n%s", out)
	}
	if !strings.Contains(out, "## logdiet rank") {
		t.Fatalf("missing heading:\n%s", out)
	}
}

func TestPlanTextVerdicts(t *testing.T) {
	ctx := ctxFrom(timedFixture)
	var buf bytes.Buffer
	PlanText(&buf, ctx, cost.Build(ctx.Rep, ctx.Model, 99, ctx.Keep))
	if !strings.Contains(buf.String(), "plan: SHORT") {
		t.Fatalf("missing SHORT verdict:\n%s", buf.String())
	}
	buf.Reset()
	PlanText(&buf, ctx, cost.Build(ctx.Rep, ctx.Model, 10, ctx.Keep))
	if !strings.Contains(buf.String(), "plan: OK") {
		t.Fatalf("missing OK verdict:\n%s", buf.String())
	}
}

func TestPlanJSONRoundTrips(t *testing.T) {
	ctx := ctxFrom(timedFixture)
	plan := cost.Build(ctx.Rep, ctx.Model, 10, ctx.Keep)
	var buf bytes.Buffer
	if err := PlanJSON(&buf, ctx, plan); err != nil {
		t.Fatal(err)
	}
	var env struct {
		Command string `json:"command"`
		Plan    struct {
			Achieved bool  `json:"achieved"`
			Items    []any `json:"items"`
		} `json:"plan"`
	}
	if err := json.Unmarshal(buf.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	if env.Command != "plan" || !env.Plan.Achieved || len(env.Plan.Items) == 0 {
		t.Fatalf("plan json = %+v", env)
	}
}

func TestRenderersAreDeterministic(t *testing.T) {
	ctx := ctxFrom(timedFixture)
	var a, b bytes.Buffer
	Text(&a, ctx)
	Text(&b, ctx)
	if a.String() != b.String() {
		t.Fatal("Text is not deterministic")
	}
	a.Reset()
	b.Reset()
	if err := JSON(&a, ctx); err != nil {
		t.Fatal(err)
	}
	if err := JSON(&b, ctx); err != nil {
		t.Fatal(err)
	}
	if a.String() != b.String() {
		t.Fatal("JSON is not deterministic")
	}
}

func TestLongTemplateTruncatedForTerminal(t *testing.T) {
	long := `{"level":"info","msg":"` + strings.Repeat("very long template ", 10) + `"}`
	var buf bytes.Buffer
	Text(&buf, ctxFrom([]string{long}))
	for _, line := range strings.Split(buf.String(), "\n") {
		if len(line) > 200 {
			t.Fatalf("terminal line too wide (%d): %q", len(line), line)
		}
	}
	if !strings.Contains(buf.String(), "…") {
		t.Fatal("long template not truncated with ellipsis")
	}
}
