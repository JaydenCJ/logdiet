// End-to-end CLI tests: run the whole command in-process with fabricated
// log files and assert on stdout, stderr, and exit codes. Everything is
// deterministic — fixture timestamps are pinned, temp dirs come from the
// test framework, and no network or subprocess is involved.
package cli

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JaydenCJ/logdiet/internal/render"
)

// run executes the CLI in-process and returns exit code, stdout, stderr.
func run(t *testing.T, stdin string, args ...string) (int, string, string) {
	t.Helper()
	var out, errBuf bytes.Buffer
	code := Run(args, strings.NewReader(stdin), &out, &errBuf)
	return code, out.String(), errBuf.String()
}

// fixture is a small but realistic mixed-format log: two heavy debug/info
// statements, a warn, an error, and an unclassifiable line, spread over a
// pinned 6-hour window.
const fixture = `{"time":"2026-07-01T00:00:00Z","level":"debug","msg":"cache miss for key sess:9f8e7d6c5b4a","shard":3}
{"time":"2026-07-01T01:00:00Z","level":"debug","msg":"cache miss for key sess:1a2b3c4d5e6f","shard":1}
{"time":"2026-07-01T02:00:00Z","level":"info","msg":"http request completed","method":"GET","path":"/api/items/42","status":200}
{"time":"2026-07-01T03:00:00Z","level":"info","msg":"http request completed","method":"POST","path":"/api/items/7","status":201}
level=warn ts=2026-07-01T04:00:00Z msg="queue depth 1500 above soft limit" queue=email
2026-07-01 05:00:00,123 ERROR com.example.Billing - charge failed for user 991
2026-07-01T06:00:00Z INFO starting worker 7 of 16
totally unstructured line
`

func fixtureFile(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "app.log")
	if err := os.WriteFile(path, []byte(fixture), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestVersionSubcommandAndFlag(t *testing.T) {
	for _, args := range [][]string{{"version"}, {"--version"}, {"-v"}} {
		code, out, _ := run(t, "", args...)
		if code != ExitOK || out != "logdiet 0.1.0\n" {
			t.Errorf("%v: code=%d out=%q", args, code, out)
		}
	}
}

func TestHelpPrintsUsage(t *testing.T) {
	code, out, _ := run(t, "", "help")
	if code != ExitOK || !strings.Contains(out, "Usage:") || !strings.Contains(out, "plan") {
		t.Fatalf("help: code=%d out=%q", code, out)
	}
}

func TestUnknownCommandExitsUsage(t *testing.T) {
	code, _, errOut := run(t, "", "frobnicate")
	if code != ExitUsage || !strings.Contains(errOut, "unknown command") {
		t.Fatalf("code=%d err=%q", code, errOut)
	}
}

func TestBarePathIsTreatedAsRank(t *testing.T) {
	path := fixtureFile(t)
	code, out, _ := run(t, "", path)
	if code != ExitOK || !strings.Contains(out, "logdiet rank —") {
		t.Fatalf("bare path: code=%d out=%q", code, out)
	}
}

func TestRankTextOnFixture(t *testing.T) {
	code, out, errOut := run(t, "", "rank", fixtureFile(t))
	if code != ExitOK {
		t.Fatalf("code=%d stderr=%q", code, errOut)
	}
	for _, want := range []string{
		"8 lines",
		"window: 2026-07-01T00:00:00Z → 2026-07-01T06:00:00Z (6h0m0s)",
		"cache miss for key sess:<hex> {shard}",
		"http request completed {method,path,status}",
		"com.example.Billing",
		"demote",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("rank output missing %q\n%s", want, out)
		}
	}
}

func TestRankMergesRepeatedStatements(t *testing.T) {
	code, out, _ := run(t, "", "rank", "--format", "json", fixtureFile(t))
	if code != ExitOK {
		t.Fatalf("code=%d", code)
	}
	var env struct {
		Totals struct {
			Lines      int64 `json:"lines"`
			Statements int   `json:"statements"`
		} `json:"totals"`
		Statements []struct {
			Template string `json:"template"`
			Count    int64  `json:"count"`
			Action   string `json:"action"`
		} `json:"statements"`
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("bad json: %v", err)
	}
	if env.Totals.Lines != 8 || env.Totals.Statements != 6 {
		t.Fatalf("totals = %+v (8 lines must fold into 6 statements)", env.Totals)
	}
	found := false
	for _, st := range env.Statements {
		if st.Template == "cache miss for key sess:<hex>" {
			found = true
			if st.Count != 2 || st.Action != "demote" {
				t.Fatalf("cache statement = %+v", st)
			}
		}
	}
	if !found {
		t.Fatal("masked cache statement not present in JSON")
	}
}

func TestRankReadsStdinByDefaultAndViaDash(t *testing.T) {
	for _, args := range [][]string{{"rank"}, {"rank", "-"}} {
		code, out, _ := run(t, fixture, args...)
		if code != ExitOK || !strings.Contains(out, "8 lines") {
			t.Errorf("%v: code=%d out=%q", args, code, out)
		}
		if !strings.Contains(out, "stdin") {
			t.Errorf("%v: input label should say stdin", args)
		}
	}
}

func TestRankReadsGzip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.log.gz")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	zw := gzip.NewWriter(f)
	if _, err := zw.Write([]byte(fixture)); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	code, out, errOut := run(t, "", "rank", path)
	if code != ExitOK {
		t.Fatalf("code=%d stderr=%q", code, errOut)
	}
	// Bytes must be the decompressed size — what a vendor would ingest.
	if !strings.Contains(out, "8 lines, "+render.Bytes(float64(len(fixture)))) {
		t.Fatalf("gzip bytes wrong (want decompressed %d):\n%s", len(fixture), out)
	}
}

func TestRankMultipleFilesAggregate(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.log")
	b := filepath.Join(dir, "b.log")
	os.WriteFile(a, []byte(`{"level":"info","msg":"served user 1"}`+"\n"), 0o644)
	os.WriteFile(b, []byte(`{"level":"info","msg":"served user 2"}`+"\n"), 0o644)
	code, out, _ := run(t, "", "rank", a, b)
	if code != ExitOK {
		t.Fatalf("code=%d", code)
	}
	if !strings.Contains(out, "2 lines") || !strings.Contains(out, "2 files") {
		t.Fatalf("multi-file aggregation:\n%s", out)
	}
	if !strings.Contains(out, "served user <n>") {
		t.Fatalf("statements must merge across files:\n%s", out)
	}
}

func TestRankMissingFileExitsRuntime(t *testing.T) {
	code, _, errOut := run(t, "", "rank", filepath.Join(t.TempDir(), "nope.log"))
	if code != ExitRuntime || errOut == "" {
		t.Fatalf("code=%d err=%q", code, errOut)
	}
}

func TestUsageErrorsExitTwo(t *testing.T) {
	// Every malformed flag must exit 2 with the flag named on stderr.
	path := fixtureFile(t)
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"rank", "--format", "yaml", path}, "--format"},
		{[]string{"rank", "--keep", "wran", path}, "--keep"},
		{[]string{"rank", "--by", "vibes", path}, "--by"},
		{[]string{"plan", "--format", "markdown", path}, "--format"},
		{[]string{"rank", "--price", "-1", path}, "--price"},
		{[]string{"plan", "--target", "0", path}, "--target"},
		{[]string{"plan", "--target", "-5", path}, "--target"},
		{[]string{"plan", "--target", "101", path}, "--target"},
	}
	for _, c := range cases {
		code, _, errOut := run(t, "", c.args...)
		if code != ExitUsage || !strings.Contains(errOut, c.want) {
			t.Errorf("%v: code=%d err=%q", c.args, code, errOut)
		}
	}
}

func TestRankByCountAndTop(t *testing.T) {
	code, out, _ := run(t, "", "rank", "--by", "count", "--top", "1", fixtureFile(t))
	if code != ExitOK {
		t.Fatalf("code=%d", code)
	}
	if !strings.Contains(out, "top 1 of 6 statements") {
		t.Fatalf("--top not applied:\n%s", out)
	}
}

func TestRankMarkdownFormat(t *testing.T) {
	code, out, _ := run(t, "", "rank", "--format", "markdown", fixtureFile(t))
	if code != ExitOK || !strings.Contains(out, "| # | Action |") {
		t.Fatalf("markdown: code=%d\n%s", code, out)
	}
}

func TestPlanReachesTarget(t *testing.T) {
	code, out, _ := run(t, "", "plan", "--target", "30", fixtureFile(t))
	if code != ExitOK || !strings.Contains(out, "plan: OK") {
		t.Fatalf("code=%d\n%s", code, out)
	}
}

func TestPlanStrictShortfallExitsOne(t *testing.T) {
	code, out, _ := run(t, "", "plan", "--target", "95", "--strict", fixtureFile(t))
	if code != ExitShort {
		t.Fatalf("code=%d, want %d\n%s", code, ExitShort, out)
	}
	if !strings.Contains(out, "plan: SHORT") {
		t.Fatalf("missing SHORT:\n%s", out)
	}
	// Without --strict the same shortfall reports but exits 0.
	code, _, _ = run(t, "", "plan", "--target", "95", fixtureFile(t))
	if code != ExitOK {
		t.Fatalf("non-strict shortfall exit = %d", code)
	}
}

func TestPlanKeepInfoWidensDemotions(t *testing.T) {
	// keep=info makes info statements survivors; only debug is demotable.
	code, out, _ := run(t, "", "plan", "--target", "100", "--keep", "info", "--format", "json", fixtureFile(t))
	if code != ExitOK {
		t.Fatalf("code=%d", code)
	}
	var env struct {
		Plan struct {
			Items []struct {
				Level string `json:"level"`
			} `json:"items"`
		} `json:"plan"`
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatal(err)
	}
	if len(env.Plan.Items) == 0 {
		t.Fatal("no items")
	}
	for _, item := range env.Plan.Items {
		if item.Level != "debug" && item.Level != "trace" {
			t.Fatalf("keep=info plan includes %q", item.Level)
		}
	}
}

func TestCustomFieldKeysFlagsWork(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "custom.log")
	os.WriteFile(path, []byte(`{"sev":"debug","note":"custom logger line 42","when":"2026-07-01T00:00:00Z"}`+"\n"), 0o644)
	code, out, _ := run(t, "", "rank",
		"--level-key", "sev", "--msg-key", "note", "--time-key", "when", path)
	if code != ExitOK {
		t.Fatalf("code=%d", code)
	}
	if !strings.Contains(out, "custom logger line <n>") {
		t.Fatalf("custom msg key not honored:\n%s", out)
	}
	if !strings.Contains(out, "debug") {
		t.Fatalf("custom level key not honored:\n%s", out)
	}
}

func TestMaxStatementsCapSurfacesOverflow(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 5; i++ {
		b.WriteString(`{"level":"info","msg":"statement variant `)
		b.WriteString(strings.Repeat("x", i+1)) // distinct templates
		b.WriteString(`"}` + "\n")
	}
	path := filepath.Join(t.TempDir(), "many.log")
	os.WriteFile(path, []byte(b.String()), 0o644)
	code, out, _ := run(t, "", "rank", "--max-statements", "2", path)
	if code != ExitOK {
		t.Fatalf("code=%d", code)
	}
	if !strings.Contains(out, "overflow:") {
		t.Fatalf("overflow notice missing:\n%s", out)
	}
	// The notice must name the configured cap, not some other number:
	// "overflow: pooled 3 lines (…) beyond the 2-statement cap".
	if !strings.Contains(out, "beyond the 2-statement cap") {
		t.Fatalf("overflow notice does not report the configured cap:\n%s", out)
	}
}
