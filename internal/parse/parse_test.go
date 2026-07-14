// Line-parsing tests across the three encodings logdiet detects: JSON,
// logfmt, and prefixed plain text. Every case is a realistic line shape
// from a mainstream logger (zap, zerolog, pino, logrus, log4j, python
// logging), because the parser's whole job is meeting logs as they are.
package parse

import (
	"reflect"
	"testing"
	"time"
)

func mustParse(t *testing.T, line string) Record {
	t.Helper()
	return Line(line, Options{})
}

// --- JSON ---

func TestJSONZapStyle(t *testing.T) {
	rec := mustParse(t, `{"level":"info","ts":"2026-07-01T10:00:00Z","msg":"served request","route":"/health"}`)
	if rec.Format != FormatJSON {
		t.Fatalf("format = %v", rec.Format)
	}
	if rec.Level != Info || rec.Message != "served request" {
		t.Fatalf("level=%v msg=%q", rec.Level, rec.Message)
	}
	if !rec.HasTime || !rec.Time.Equal(time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC)) {
		t.Fatalf("time = %v (has=%v)", rec.Time, rec.HasTime)
	}
	if !reflect.DeepEqual(rec.Keys, []string{"route"}) {
		t.Fatalf("keys = %v", rec.Keys)
	}
}

func TestJSONPinoNumericLevelAndEpochMillis(t *testing.T) {
	rec := mustParse(t, `{"level":30,"time":1751364000000,"pid":42,"hostname":"web-1","msg":"request completed"}`)
	if rec.Level != Info {
		t.Fatalf("level = %v, want Info (pino 30)", rec.Level)
	}
	if !rec.HasTime || rec.Time.Year() != 2025 {
		t.Fatalf("epoch millis not parsed: %v", rec.Time)
	}
	if !reflect.DeepEqual(rec.Keys, []string{"hostname", "pid"}) {
		t.Fatalf("keys = %v (must be sorted, level/time/msg excluded)", rec.Keys)
	}
}

func TestJSONNestedECSLevel(t *testing.T) {
	// Elastic Common Schema nests the level under "log.level".
	rec := mustParse(t, `{"log":{"level":"warn"},"message":"disk pressure","host":"node-3"}`)
	if rec.Level != Warn || rec.Message != "disk pressure" {
		t.Fatalf("level=%v msg=%q", rec.Level, rec.Message)
	}
	for _, k := range rec.Keys {
		if k == "log" {
			t.Fatal("consumed level container leaked into keys")
		}
	}
}

func TestJSONSeverityAndTimestampAliases(t *testing.T) {
	rec := mustParse(t, `{"severity":"ERROR","@timestamp":"2026-07-01 10:00:00.500","message":"boom"}`)
	if rec.Level != Error {
		t.Fatalf("level = %v", rec.Level)
	}
	if !rec.HasTime || rec.Time.Nanosecond() != 500_000_000 {
		t.Fatalf("time = %v", rec.Time)
	}
}

func TestJSONEpochSecondsFloat(t *testing.T) {
	rec := mustParse(t, `{"level":"debug","ts":1751364000.25,"msg":"tick"}`)
	if !rec.HasTime {
		t.Fatal("float epoch seconds not parsed")
	}
	if rec.Time.Nanosecond() != 250_000_000 {
		t.Fatalf("fractional seconds lost: %v", rec.Time)
	}
}

func TestJSONExtraKeyOptionWinsOverDefaults(t *testing.T) {
	opts := Options{ExtraMsgKeys: []string{"description"}}
	rec := Line(`{"level":"info","description":"custom logger","msg":"ignored"}`, opts)
	if rec.Message != "custom logger" {
		t.Fatalf("msg = %q, want the --msg-key field to win", rec.Message)
	}
}

func TestInvalidJSONFallsBackToText(t *testing.T) {
	rec := mustParse(t, `{"level":"info","msg":`)
	if rec.Format != FormatText {
		t.Fatalf("format = %v, want text fallback", rec.Format)
	}
}

// --- logfmt ---

func TestLogfmtBasic(t *testing.T) {
	rec := mustParse(t, `level=info msg="user logged in" user_id=991 ip=10.0.0.5`)
	if rec.Format != FormatLogfmt {
		t.Fatalf("format = %v", rec.Format)
	}
	if rec.Level != Info || rec.Message != "user logged in" {
		t.Fatalf("level=%v msg=%q", rec.Level, rec.Message)
	}
	if !reflect.DeepEqual(rec.Keys, []string{"ip", "user_id"}) {
		t.Fatalf("keys = %v", rec.Keys)
	}
}

func TestLogfmtEscapedQuotes(t *testing.T) {
	rec := mustParse(t, `level=error msg="cannot parse \"config.yml\"" attempt=2`)
	if rec.Message != `cannot parse "config.yml"` {
		t.Fatalf("msg = %q", rec.Message)
	}
}

func TestLogfmtBareWordsBecomeMessage(t *testing.T) {
	// Go's stdlib log + key=value suffixes is a common hybrid.
	rec := mustParse(t, `starting server addr=127.0.0.1:8080 tls=false`)
	if rec.Message != "starting server" {
		t.Fatalf("msg = %q", rec.Message)
	}
	if !reflect.DeepEqual(rec.Keys, []string{"addr", "tls"}) {
		t.Fatalf("keys = %v", rec.Keys)
	}
}

func TestLogfmtTimestampField(t *testing.T) {
	rec := mustParse(t, `ts=2026-07-01T10:00:00Z level=warn msg="queue slow"`)
	if !rec.HasTime || rec.Time.Hour() != 10 {
		t.Fatalf("time = %v (has=%v)", rec.Time, rec.HasTime)
	}
}

// --- plain text ---

func TestTextRFC3339PrefixAndLevel(t *testing.T) {
	rec := mustParse(t, `2026-07-01T10:00:00Z INFO starting worker 7 of 16`)
	if rec.Format != FormatText || rec.Level != Info {
		t.Fatalf("format=%v level=%v", rec.Format, rec.Level)
	}
	if rec.Message != "starting worker 7 of 16" {
		t.Fatalf("msg = %q", rec.Message)
	}
	if !rec.HasTime {
		t.Fatal("timestamp prefix not parsed")
	}
}

func TestTextBracketedTimestampAndLevel(t *testing.T) {
	rec := mustParse(t, `[2026-07-01 10:00:00] [ERROR] connection reset`)
	if rec.Level != Error || rec.Message != "connection reset" {
		t.Fatalf("level=%v msg=%q", rec.Level, rec.Message)
	}
	if !rec.HasTime {
		t.Fatal("bracketed timestamp not parsed")
	}
}

func TestTextJavaLoggerConvention(t *testing.T) {
	// log4j/python style: timestamp LEVEL dotted.logger - message
	rec := mustParse(t, `2026-07-01 10:00:00,123 WARN com.example.Billing - charge failed for user 991`)
	if rec.Level != Warn {
		t.Fatalf("level = %v", rec.Level)
	}
	if rec.Logger != "com.example.Billing" {
		t.Fatalf("logger = %q", rec.Logger)
	}
	if rec.Message != "charge failed for user 991" {
		t.Fatalf("msg = %q", rec.Message)
	}
	if !rec.HasTime || rec.Time.Nanosecond() != 123_000_000 {
		t.Fatalf("comma-millis timestamp: %v", rec.Time)
	}
}

func TestTextLevelTokenAndUnstructuredLines(t *testing.T) {
	// "Error" leading a bare line IS a level token and must be consumed.
	rec := mustParse(t, `Error while reading; will retry`)
	if rec.Level != Error || rec.Message != "while reading; will retry" {
		t.Fatalf("level=%v msg=%q", rec.Level, rec.Message)
	}
	// Fully unstructured lines classify as Unknown with the text intact.
	rec = mustParse(t, `completely unstructured line`)
	if rec.Level != Unknown || rec.HasTime || rec.Message != "completely unstructured line" {
		t.Fatalf("unstructured: %+v", rec)
	}
}

func TestBytesCountRawLinePlusNewline(t *testing.T) {
	line := `{"level":"info","msg":"x"}`
	rec := mustParse(t, line)
	if rec.Bytes != len(line)+1 {
		t.Fatalf("bytes = %d, want %d (raw + newline)", rec.Bytes, len(line)+1)
	}
	// Empty lines still occupy a newline and must be accounted for.
	rec = mustParse(t, "")
	if rec.Bytes != 1 || rec.Level != Unknown || rec.Message != "" {
		t.Fatalf("empty line parsed oddly: %+v", rec)
	}
}

// --- timestamp unit heuristics ---

func TestParseEpochUnitGuessing(t *testing.T) {
	want := time.Date(2025, 7, 1, 10, 0, 0, 0, time.UTC)
	sec := float64(want.Unix())
	cases := []float64{sec, sec * 1e3, sec * 1e6, sec * 1e9}
	for _, v := range cases {
		got, ok := parseEpoch(v)
		if !ok || !got.Equal(want) {
			t.Errorf("parseEpoch(%g) = %v, %v; want %v", v, got, ok, want)
		}
	}
	// Zero and negative values must not become window edges.
	if _, ok := parseEpoch(0); ok {
		t.Error("epoch 0 accepted")
	}
	if _, ok := parseEpoch(-5); ok {
		t.Error("negative epoch accepted")
	}
}

func TestParseTimeStringLayouts(t *testing.T) {
	cases := []string{
		"2026-07-01T10:00:00Z",
		"2026-07-01T10:00:00.123456789Z",
		"2026-07-01T10:00:00+09:00",
		"2026-07-01 10:00:00",
		"2026-07-01 10:00:00.123",
		"2026-07-01 10:00:00,123",
		"2026/07/01 10:00:00",
	}
	for _, in := range cases {
		if _, ok := parseTimeString(in); !ok {
			t.Errorf("parseTimeString(%q) failed", in)
		}
	}
	if _, ok := parseTimeString("yesterday"); ok {
		t.Error("parseTimeString accepted prose")
	}
}
