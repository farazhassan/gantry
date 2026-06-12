package eval

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// Dataset returns the cases to evaluate.
type Dataset interface {
	Cases(ctx context.Context) ([]Case, error)
}

// JSONLDataset loads cases from a JSONL file. Each line is a JSON-encoded Case.
// Blank lines are ignored.
type JSONLDataset string

// Cases reads and parses the file.
func (j JSONLDataset) Cases(_ context.Context) ([]Case, error) {
	f, err := os.Open(string(j))
	if err != nil {
		return nil, fmt.Errorf("eval: open dataset %q: %w", string(j), err)
	}
	defer f.Close()

	var out []Case
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024) // tolerate long lines
	lineNo := 0
	for sc.Scan() {
		lineNo++
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var c Case
		if err := json.Unmarshal([]byte(line), &c); err != nil {
			return nil, fmt.Errorf("eval: parse %s line %d: %w", string(j), lineNo, err)
		}
		out = append(out, c)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// SliceDataset is an in-memory Dataset useful for tests and programmatic
// case generation.
type SliceDataset []Case

func (s SliceDataset) Cases(_ context.Context) ([]Case, error) {
	out := make([]Case, len(s))
	copy(out, s)
	return out, nil
}
