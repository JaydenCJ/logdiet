// Level normalization tests: every spelling in the acceptance table, the
// pino/bunyan numeric convention, and the strictness of --keep parsing.
package parse

import "testing"

func TestParseLevelCommonSpellings(t *testing.T) {
	cases := map[string]Level{
		"trace": Trace, "TRACE": Trace, "verbose": Trace,
		"debug": Debug, "DBG": Debug, "fine": Debug,
		"info": Info, "INFO": Info, "notice": Info, "informational": Info,
		"warn": Warn, "WARNING": Warn,
		"error": Error, "err": Error, "SEVERE": Error,
		"fatal": Fatal, "critical": Fatal, "panic": Fatal, "emerg": Fatal,
	}
	for in, want := range cases {
		if got := ParseLevel(in); got != want {
			t.Errorf("ParseLevel(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestParseLevelToleratesDecoration(t *testing.T) {
	// Text logs wrap levels in brackets or add colons; all must normalize.
	for _, in := range []string{"[INFO]", "info:", "(info)", " info "} {
		if got := ParseLevel(in); got != Info {
			t.Errorf("ParseLevel(%q) = %v, want Info", in, got)
		}
	}
}

func TestParseLevelNumericPino(t *testing.T) {
	cases := map[string]Level{"10": Trace, "20": Debug, "30": Info, "40": Warn, "50": Error, "60": Fatal}
	for in, want := range cases {
		if got := ParseLevel(in); got != want {
			t.Errorf("ParseLevel(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestParseLevelUnknownForGarbage(t *testing.T) {
	for _, in := range []string{"", "banana", "35", "infoish", "level"} {
		if got := ParseLevel(in); got != Unknown {
			t.Errorf("ParseLevel(%q) = %v, want Unknown", in, got)
		}
	}
}

func TestLevelOrderingAndStringRoundTrip(t *testing.T) {
	// The demotion rule is `level < keep`; the enum order must escalate,
	// and String() must survive a round trip through ParseLevel.
	order := []Level{Unknown, Trace, Debug, Info, Warn, Error, Fatal}
	for i := 1; i < len(order); i++ {
		if !(order[i-1] < order[i]) {
			t.Fatalf("level order broken at %v < %v", order[i-1], order[i])
		}
		if got := ParseLevel(order[i].String()); got != order[i] {
			t.Errorf("ParseLevel(%v.String()) = %v", order[i], got)
		}
	}
}

func TestParseKeepLevelRejectsUnknown(t *testing.T) {
	// A typo in --keep must be an error, not a silent Unknown that would
	// make every statement demotable.
	if _, ok := ParseKeepLevel("wran"); ok {
		t.Fatal("ParseKeepLevel accepted a typo")
	}
	if l, ok := ParseKeepLevel("warn"); !ok || l != Warn {
		t.Fatalf("ParseKeepLevel(warn) = %v, %v", l, ok)
	}
}
