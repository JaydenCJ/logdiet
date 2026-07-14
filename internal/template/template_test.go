// Masking tests: one test per rule family plus the interaction cases that
// historically break template extractors (UUIDs vs hex vs numbers, paths,
// apostrophes). The idempotence test at the end is the load-bearing one —
// aggregation keys must be fixed points of Mask.
package template

import (
	"regexp"
	"testing"
)

func assertMask(t *testing.T, in, want string) {
	t.Helper()
	if got := Mask(in); got != want {
		t.Errorf("Mask(%q) = %q, want %q", in, got, want)
	}
}

func TestMaskBareNumbers(t *testing.T) {
	assertMask(t, "retried 3 times with 12 workers", "retried <n> times with <n> workers")
	assertMask(t, "loss 0.0231 after epoch 7", "loss <n> after epoch <n>")
	assertMask(t, "rate 1.5e-3 applied", "rate <n> applied")
}

func TestMaskKeepsDigitsInsideIdentifiers(t *testing.T) {
	// v2, sha256, utf8: digits glued to letters are part of the name.
	assertMask(t, "using sha256 digest on utf8 input", "using sha256 digest on utf8 input")
	assertMask(t, "api v2 enabled", "api v2 enabled")
}

func TestMaskDurations(t *testing.T) {
	assertMask(t, "request took 12ms", "request took <dur>")
	assertMask(t, "slow query 1.5s in pool", "slow query <dur> in pool")
	assertMask(t, "gc pause 250µs", "gc pause <dur>")
}

func TestMaskByteSizes(t *testing.T) {
	assertMask(t, "uploaded 4.2 MiB payload", "uploaded <size> payload")
	assertMask(t, "heap grew to 512MB", "heap grew to <size>")
}

func TestMaskUUIDBeforeHexRules(t *testing.T) {
	// A UUID must become one <uuid>, not shredded into <hex>/<n> pieces.
	assertMask(t, "order a1b2c3d4-e5f6-4a90-abcd-ef1234567890 created",
		"order <uuid> created")
}

func TestMaskHexIdentifiers(t *testing.T) {
	assertMask(t, "trace 9f8e7d6c5b4a started", "trace <hex> started")
	assertMask(t, "commit e3b0c44298fc1c14 pushed", "commit <hex> pushed")
	assertMask(t, "pointer 0xDEADBEEF freed", "pointer <hex> freed")
	// "feed", "decade": too short or letter-only — real words survive.
	assertMask(t, "feed the decade cache", "feed the decade cache")
	// An all-digit 12+ run is still an ID: it must join the <hex> bucket,
	// or one statement would split into <hex> and <n> templates.
	assertMask(t, "trace 782641950360 started", "trace <hex> started")
}

func TestMaskIPv4WithPort(t *testing.T) {
	assertMask(t, "connect to 10.0.0.5:5432 failed", "connect to <ip> failed")
	assertMask(t, "peer 192.168.1.77 joined", "peer <ip> joined")
}

func TestMaskEmail(t *testing.T) {
	assertMask(t, "invite sent to ops@example.test", "invite sent to <email>")
}

func TestMaskEmbeddedTimestamps(t *testing.T) {
	assertMask(t, "retry at 2026-07-12T10:00:00Z scheduled", "retry at <ts> scheduled")
	assertMask(t, "job ran on 2026-07-12 late", "job ran on <date> late")
	assertMask(t, "cron fired at 10:15:00 sharp", "cron fired at <time> sharp")
}

func TestMaskDoubleQuotedStrings(t *testing.T) {
	assertMask(t, `open "config.yml" failed`, `open "<str>" failed`)
	assertMask(t, `open "a \"quoted\" name" failed`, `open "<str>" failed`)
}

func TestMaskSingleQuotedStringsRespectApostrophes(t *testing.T) {
	// The apostrophe in "can't" must not pair with the opening quote.
	assertMask(t, "can't open 'data.bin'", "can't open '<str>'")
	assertMask(t, "found 'a' and 'b' entries", "found '<str>' and '<str>' entries")
}

func TestMaskPathSegments(t *testing.T) {
	assertMask(t, "GET /api/items/42/orders returned 200",
		"GET /api/items/<*>/orders returned <n>")
	assertMask(t, "served /assets/logo.png fine", "served /assets/logo.png fine")
	assertMask(t, "GET /api/items/9f8e7d6c5b4a1234 done", "GET /api/items/<*> done")
	// Paths glued to a key= prefix are still route-shaped.
	assertMask(t, "handler path=/api/items/42 matched", "handler path=/api/items/<*> matched")
}

func TestMaskBooleans(t *testing.T) {
	assertMask(t, "cache lookup hit=false", "cache lookup hit=<bool>")
	assertMask(t, "cache lookup hit=true", "cache lookup hit=<bool>")
	// Prose "true" without a key= stays a word.
	assertMask(t, "this holds true here", "this holds true here")
}

func TestMaskIdempotent(t *testing.T) {
	assertMask(t, "", "") // the empty message is its own fixed point
	inputs := []string{
		"retried 3 times with 12 workers",
		"order a1b2c3d4-e5f6-4a90-abcd-ef1234567890 created at 2026-07-12T10:00:00Z",
		`open "config.yml" from 10.0.0.5:5432 took 12ms`,
		"GET /api/items/42/orders?limit=10 returned 200 in 1.5s",
		"can't open 'data.bin' (4.2 MiB) for ops@example.test",
	}
	for _, in := range inputs {
		once := Mask(in)
		twice := Mask(once)
		if once != twice {
			t.Errorf("Mask not idempotent:\n in: %q\n 1x: %q\n 2x: %q", in, once, twice)
		}
	}
}

func TestKeyIncludesEveryIdentityComponent(t *testing.T) {
	base := Key("info", "app", "hello <n>", []string{"a", "b"})
	variants := []string{
		Key("warn", "app", "hello <n>", []string{"a", "b"}),
		Key("info", "other", "hello <n>", []string{"a", "b"}),
		Key("info", "app", "goodbye <n>", []string{"a", "b"}),
		Key("info", "app", "hello <n>", []string{"a"}),
	}
	for i, v := range variants {
		if v == base {
			t.Errorf("variant %d collided with base key", i)
		}
	}
	// Separator check: "ab"+"c" vs "a"+"bc" must not collide.
	if Key("info", "ab", "c", nil) == Key("info", "a", "bc", nil) {
		t.Fatal("key fields bleed into each other")
	}
}

func TestIDStableAndWellFormed(t *testing.T) {
	k := Key("info", "", "hello <n>", nil)
	id1, id2 := ID(k), ID(k)
	if id1 != id2 {
		t.Fatalf("ID not deterministic: %q vs %q", id1, id2)
	}
	if !regexp.MustCompile(`^s:[0-9a-f]{8}$`).MatchString(id1) {
		t.Fatalf("ID format = %q", id1)
	}
	if ID(Key("warn", "", "hello <n>", nil)) == id1 {
		t.Fatal("different keys hashed to the same id (suspicious for these inputs)")
	}
}
