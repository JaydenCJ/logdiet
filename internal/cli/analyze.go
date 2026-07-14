package cli

import (
	"bufio"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/JaydenCJ/logdiet/internal/aggregate"
	"github.com/JaydenCJ/logdiet/internal/parse"
)

// maxLineBytes bounds a single log line. 4 MiB comfortably covers even
// pathological single-line stack traces; anything longer is split by the
// scanner's own error, which we surface with the file name.
const maxLineBytes = 4 << 20

// analyze reads every input, feeds the aggregator, and returns the sorted
// report plus the display names of the inputs.
func analyze(paths []string, stdin io.Reader, popts parse.Options, maxStmts int, by aggregate.SortKey) (*aggregate.Report, []string, error) {
	agg := aggregate.New(maxStmts)
	var inputs []string

	if len(paths) == 0 {
		if err := scanInto(agg, stdin, popts, "stdin"); err != nil {
			return nil, nil, err
		}
	}
	for _, p := range paths {
		if p == "-" {
			inputs = append(inputs, "stdin")
			if err := scanInto(agg, stdin, popts, "stdin"); err != nil {
				return nil, nil, err
			}
			continue
		}
		inputs = append(inputs, p)
		if err := scanFile(agg, p, popts); err != nil {
			return nil, nil, err
		}
	}
	return agg.Finish(by), inputs, nil
}

// scanFile opens one input file, transparently decompressing .gz.
func scanFile(agg *aggregate.Aggregator, path string, popts parse.Options) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	var r io.Reader = f
	if strings.HasSuffix(path, ".gz") {
		gz, err := gzip.NewReader(f)
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		defer gz.Close()
		r = gz
	}
	return scanInto(agg, r, popts, path)
}

// scanInto streams lines from r into the aggregator. Line bytes are
// counted from the decompressed text (what your vendor would ingest), not
// the on-disk .gz size.
func scanInto(agg *aggregate.Aggregator, r io.Reader, popts parse.Options, name string) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), maxLineBytes)
	for sc.Scan() {
		line := sc.Text()
		agg.Add(parse.Line(line, popts), line)
	}
	if err := sc.Err(); err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	return nil
}
