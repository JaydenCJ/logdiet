// Package cli implements the logdiet command-line interface. Run takes
// argv plus explicit stdin/stdout/stderr and returns an exit code, so the
// whole surface is testable in-process without building a binary.
package cli

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/JaydenCJ/logdiet/internal/aggregate"
	"github.com/JaydenCJ/logdiet/internal/cost"
	"github.com/JaydenCJ/logdiet/internal/parse"
	"github.com/JaydenCJ/logdiet/internal/render"
	"github.com/JaydenCJ/logdiet/internal/version"
)

// Exit codes. Documented in the README; `plan --strict` uses ExitShort as
// its machine-readable verdict for budget gates.
const (
	ExitOK      = 0
	ExitShort   = 1
	ExitUsage   = 2
	ExitRuntime = 3
)

// Run dispatches argv and returns the process exit code.
func Run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		return runRank(nil, stdin, stdout, stderr)
	}
	switch args[0] {
	case "rank":
		return runRank(args[1:], stdin, stdout, stderr)
	case "plan":
		return runPlan(args[1:], stdin, stdout, stderr)
	case "version", "--version", "-v":
		fmt.Fprintf(stdout, "logdiet %s\n", version.Version)
		return ExitOK
	case "help", "--help", "-h":
		usage(stdout)
		return ExitOK
	default:
		if strings.HasPrefix(args[0], "-") || fileLike(args[0]) {
			// Bare flags or a bare path: treat as `rank …`.
			return runRank(args, stdin, stdout, stderr)
		}
		fmt.Fprintf(stderr, "logdiet: unknown command %q\n\n", args[0])
		usage(stderr)
		return ExitUsage
	}
}

// fileLike reports whether a bare first argument reads as an input rather
// than a mistyped subcommand: "-" (stdin), anything with a path separator
// or a dot, or the name of a file that actually exists (so an
// extensionless log like `logdiet mylog` still works).
func fileLike(s string) bool {
	if s == "-" || strings.ContainsAny(s, "./\\") {
		return true
	}
	_, err := os.Stat(s)
	return err == nil
}

// multiFlag is a repeatable string flag.
type multiFlag []string

func (m *multiFlag) String() string     { return strings.Join(*m, ",") }
func (m *multiFlag) Set(v string) error { *m = append(*m, v); return nil }

// sharedFlags are common to rank and plan.
type sharedFlags struct {
	format    string
	keep      string
	price     float64
	levelKeys multiFlag
	msgKeys   multiFlag
	timeKeys  multiFlag
	maxStmts  int
}

func (s *sharedFlags) register(fs *flag.FlagSet) {
	fs.StringVar(&s.format, "format", "text", "output format")
	fs.StringVar(&s.keep, "keep", "warn", "lowest level that must stay in production; below it is demotable")
	fs.Float64Var(&s.price, "price", cost.DefaultPricePerGB, "ingest price in USD per GB")
	fs.Var(&s.levelKeys, "level-key", "extra JSON/logfmt field carrying the level (repeatable)")
	fs.Var(&s.msgKeys, "msg-key", "extra field carrying the message (repeatable)")
	fs.Var(&s.timeKeys, "time-key", "extra field carrying the timestamp (repeatable)")
	fs.IntVar(&s.maxStmts, "max-statements", 0, "cap on distinct statements held in memory (0 = default 100000)")
}

// validate turns shared flags into analysis inputs, or a usage error.
func (s *sharedFlags) validate(formats []string) (parse.Level, parse.Options, error) {
	ok := false
	for _, f := range formats {
		if s.format == f {
			ok = true
			break
		}
	}
	if !ok {
		return 0, parse.Options{}, fmt.Errorf("unknown --format %q (want %s)", s.format, strings.Join(formats, ", "))
	}
	keep, valid := parse.ParseKeepLevel(s.keep)
	if !valid {
		return 0, parse.Options{}, fmt.Errorf("unknown --keep level %q (want trace, debug, info, warn, error, or fatal)", s.keep)
	}
	if s.price < 0 {
		return 0, parse.Options{}, fmt.Errorf("--price must be >= 0, got %v", s.price)
	}
	opts := parse.Options{
		ExtraLevelKeys: s.levelKeys,
		ExtraMsgKeys:   s.msgKeys,
		ExtraTimeKeys:  s.timeKeys,
	}
	return keep, opts, nil
}

func runRank(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("rank", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var sf sharedFlags
	sf.register(fs)
	top := fs.Int("top", 20, "number of statements to show (0 = all)")
	by := fs.String("by", "bytes", "ranking key: bytes or count")
	if err := fs.Parse(args); err != nil {
		return ExitUsage
	}
	keep, popts, err := sf.validate([]string{"text", "json", "markdown"})
	if err != nil {
		fmt.Fprintf(stderr, "logdiet: %v\n", err)
		return ExitUsage
	}
	sortKey := aggregate.ByBytes
	switch *by {
	case "bytes":
	case "count":
		sortKey = aggregate.ByCount
	default:
		fmt.Fprintf(stderr, "logdiet: unknown --by %q (want bytes or count)\n", *by)
		return ExitUsage
	}

	rep, inputs, err := analyze(fs.Args(), stdin, popts, sf.maxStmts, sortKey)
	if err != nil {
		fmt.Fprintf(stderr, "logdiet: %v\n", err)
		return ExitRuntime
	}
	ctx := render.Context{
		Rep:    rep,
		Model:  cost.NewModel(rep, sf.price),
		Keep:   keep,
		Top:    *top,
		By:     sortKey,
		Inputs: inputs,
	}
	switch sf.format {
	case "json":
		if err := render.JSON(stdout, ctx); err != nil {
			fmt.Fprintf(stderr, "logdiet: %v\n", err)
			return ExitRuntime
		}
	case "markdown":
		render.Markdown(stdout, ctx)
	default:
		render.Text(stdout, ctx)
	}
	return ExitOK
}

func runPlan(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("plan", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var sf sharedFlags
	sf.register(fs)
	target := fs.Float64("target", 40, "byte-reduction target in percent")
	strict := fs.Bool("strict", false, "exit 1 when the target is unreachable by demotion alone")
	if err := fs.Parse(args); err != nil {
		return ExitUsage
	}
	keep, popts, err := sf.validate([]string{"text", "json"})
	if err != nil {
		fmt.Fprintf(stderr, "logdiet: %v\n", err)
		return ExitUsage
	}
	if *target <= 0 || *target > 100 {
		fmt.Fprintf(stderr, "logdiet: --target must be in (0, 100], got %v\n", *target)
		return ExitUsage
	}

	rep, inputs, err := analyze(fs.Args(), stdin, popts, sf.maxStmts, aggregate.ByBytes)
	if err != nil {
		fmt.Fprintf(stderr, "logdiet: %v\n", err)
		return ExitRuntime
	}
	model := cost.NewModel(rep, sf.price)
	plan := cost.Build(rep, model, *target, keep)
	ctx := render.Context{Rep: rep, Model: model, Keep: keep, Inputs: inputs}
	if sf.format == "json" {
		if err := render.PlanJSON(stdout, ctx, plan); err != nil {
			fmt.Fprintf(stderr, "logdiet: %v\n", err)
			return ExitRuntime
		}
	} else {
		render.PlanText(stdout, ctx, plan)
	}
	if *strict && !plan.Achieved {
		return ExitShort
	}
	return ExitOK
}

func usage(w io.Writer) {
	fmt.Fprintf(w, `logdiet %s — a cost-ranked hit list for your log volume

Usage:
  logdiet [rank] [flags] [file…]    rank statements by bytes (default; "-" or no file = stdin)
  logdiet plan   [flags] [file…]    demotion hit list to reach a byte-reduction target
  logdiet version                   print the version

Shared flags:
  --format FORMAT      text (default), json; rank also accepts markdown
  --keep LEVEL         lowest level kept in production (default warn);
                       statements below it are demotion candidates
  --price USD          ingest price per GB for $ estimates (default %.2f)
  --level-key KEY      extra field name carrying the level (repeatable)
  --msg-key KEY        extra field name carrying the message (repeatable)
  --time-key KEY       extra field name carrying the timestamp (repeatable)
  --max-statements N   cap distinct statements held in memory (default 100000)

Rank flags:
  --top N              statements to show, 0 = all (default 20)
  --by KEY             ranking key: bytes (default) or count

Plan flags:
  --target PCT         byte-reduction target in percent (default 40)
  --strict             exit 1 when demotion alone cannot reach the target

Inputs may be plain files, .gz files, or stdin; JSON, logfmt, and prefixed
plain-text lines are detected per line, so mixed streams are fine.

Exit codes: 0 ok · 1 plan --strict shortfall · 2 usage error · 3 runtime error
`, version.Version, cost.DefaultPricePerGB)
}
