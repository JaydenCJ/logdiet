// Aggregation tests: grouping, totals conservation, the time window, the
// statement cap, and deterministic ordering. The invariant that matters
// most is conservation — every input byte lands somewhere the report can
// account for.
package aggregate

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/JaydenCJ/logdiet/internal/parse"
)

func addLine(a *Aggregator, line string) {
	a.Add(parse.Line(line, parse.Options{}), line)
}

func TestGroupsVariantsOfOneStatement(t *testing.T) {
	a := New(0)
	addLine(a, `{"level":"info","msg":"served user 1"}`)
	addLine(a, `{"level":"info","msg":"served user 2"}`)
	addLine(a, `{"level":"info","msg":"served user 300"}`)
	rep := a.Finish(ByBytes)
	if len(rep.Statements) != 1 {
		t.Fatalf("statements = %d, want 1 (masking should merge)", len(rep.Statements))
	}
	st := rep.Statements[0]
	if st.Count != 3 || st.Template != "served user <n>" {
		t.Fatalf("stat = %+v", st)
	}
}

func TestSameMessageDifferentLevelStaysSeparate(t *testing.T) {
	a := New(0)
	addLine(a, `{"level":"info","msg":"shutdown requested"}`)
	addLine(a, `{"level":"warn","msg":"shutdown requested"}`)
	rep := a.Finish(ByBytes)
	if len(rep.Statements) != 2 {
		t.Fatalf("statements = %d, want 2 (level is identity)", len(rep.Statements))
	}
}

func TestTotalsConservation(t *testing.T) {
	a := New(0)
	lines := []string{
		`{"level":"debug","msg":"a"}`,
		`level=info msg="b"`,
		`plain c`,
		``,
	}
	var want int64
	for _, l := range lines {
		addLine(a, l)
		want += int64(len(l) + 1)
	}
	rep := a.Finish(ByBytes)
	if rep.Bytes != want || rep.Lines != int64(len(lines)) {
		t.Fatalf("totals = %d bytes / %d lines, want %d / %d", rep.Bytes, rep.Lines, want, len(lines))
	}
	var sum int64
	for _, st := range rep.Statements {
		sum += st.Bytes
	}
	if sum+rep.OverflowBytes != rep.Bytes {
		t.Fatalf("statement bytes %d + overflow %d != total %d", sum, rep.OverflowBytes, rep.Bytes)
	}
}

func TestPerLevelAndPerFormatTallies(t *testing.T) {
	a := New(0)
	addLine(a, `{"level":"debug","msg":"x"}`)
	addLine(a, `{"level":"debug","msg":"y"}`)
	addLine(a, `level=error msg="z"`)
	addLine(a, `plain`)
	rep := a.Finish(ByBytes)
	if rep.LevelLines["debug"] != 2 || rep.LevelLines["error"] != 1 || rep.LevelLines["unknown"] != 1 {
		t.Fatalf("level lines = %v", rep.LevelLines)
	}
	if rep.Formats["json"] != 2 || rep.Formats["logfmt"] != 1 || rep.Formats["text"] != 1 {
		t.Fatalf("formats = %v", rep.Formats)
	}
}

func TestWindowSpansFirstToLast(t *testing.T) {
	a := New(0)
	addLine(a, `{"level":"info","time":"2026-07-01T12:00:00Z","msg":"mid"}`)
	addLine(a, `{"level":"info","time":"2026-07-01T00:00:00Z","msg":"first"}`)
	addLine(a, `{"level":"info","time":"2026-07-01T18:00:00Z","msg":"last"}`)
	rep := a.Finish(ByBytes)
	w, ok := rep.Window()
	if !ok || w != 18*time.Hour {
		t.Fatalf("window = %v, %v", w, ok)
	}
	if rep.First.Hour() != 0 || rep.Last.Hour() != 18 {
		t.Fatalf("edges = %v .. %v", rep.First, rep.Last)
	}
}

func TestWindowUnderOneMinuteIsRejected(t *testing.T) {
	// Extrapolating a daily rate from 30 seconds of logs is noise.
	a := New(0)
	addLine(a, `{"level":"info","time":"2026-07-01T00:00:00Z","msg":"a"}`)
	addLine(a, `{"level":"info","time":"2026-07-01T00:00:30Z","msg":"b"}`)
	if _, ok := a.Finish(ByBytes).Window(); ok {
		t.Fatal("sub-minute window must not produce a rate")
	}
}

func TestNoTimestampsMeansNoWindow(t *testing.T) {
	a := New(0)
	addLine(a, `plain one`)
	if _, ok := a.Finish(ByBytes).Window(); ok {
		t.Fatal("window without timestamps")
	}
}

func TestStatementCapOverflows(t *testing.T) {
	a := New(2)
	addLine(a, `{"level":"info","msg":"alpha route"}`)
	addLine(a, `{"level":"info","msg":"beta route"}`)
	addLine(a, `{"level":"info","msg":"gamma route"}`) // over the cap
	addLine(a, `{"level":"info","msg":"alpha route"}`) // existing bucket still grows
	rep := a.Finish(ByBytes)
	if len(rep.Statements) != 2 {
		t.Fatalf("statements = %d, want the cap of 2", len(rep.Statements))
	}
	if rep.OverflowLines != 1 || rep.StatementCap != 2 {
		t.Fatalf("overflow = %+v", rep)
	}
	if rep.Lines != 4 {
		t.Fatalf("total lines = %d, want 4 (overflow still counted)", rep.Lines)
	}
}

func TestSortByBytesDescWithStableTieBreak(t *testing.T) {
	a := New(0)
	// Same byte size, different templates: order must be alphabetical.
	addLine(a, `{"level":"info","msg":"bbb"}`)
	addLine(a, `{"level":"info","msg":"aaa"}`)
	rep := a.Finish(ByBytes)
	if rep.Statements[0].Template != "aaa" || rep.Statements[1].Template != "bbb" {
		t.Fatalf("tie-break order: %q, %q", rep.Statements[0].Template, rep.Statements[1].Template)
	}
}

func TestSortByCount(t *testing.T) {
	a := New(0)
	addLine(a, `{"level":"info","msg":"short but frequent"}`)
	addLine(a, `{"level":"info","msg":"short but frequent"}`)
	addLine(a, fmt.Sprintf(`{"level":"info","msg":"one huge line %s"}`, strings.Repeat("x", 500)))
	rep := a.Finish(ByCount)
	if rep.Statements[0].Template != "short but frequent" {
		t.Fatalf("--by count must lead with the frequent statement, got %q", rep.Statements[0].Template)
	}
	if rep2 := a.Finish(ByBytes); rep2.Statements[0].Count != 1 {
		t.Fatalf("--by bytes must lead with the huge statement")
	}
}

func TestSampleIsFirstRawLineTruncated(t *testing.T) {
	a := New(0)
	long := `{"level":"info","msg":"padding ` + strings.Repeat("é", 600) + `"}`
	addLine(a, long)
	rep := a.Finish(ByBytes)
	sample := rep.Statements[0].Sample
	if !strings.HasSuffix(sample, "…") {
		t.Fatalf("long sample not truncated: %q", sample[:40])
	}
	if len(sample) > 410 {
		t.Fatalf("sample too long: %d bytes", len(sample))
	}
	if !strings.HasPrefix(sample, `{"level":"info","msg":"padding `) {
		t.Fatalf("sample lost its prefix: %q", sample[:40])
	}
	// Truncation must not split the multi-byte é.
	if !strings.HasSuffix(strings.TrimSuffix(sample, "…"), "é") {
		t.Fatalf("sample cut mid-rune: % x", sample[len(sample)-6:])
	}
}
