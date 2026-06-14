// components/ask/cli.go
package ask

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// skipToken on its own line declines (skips) a single question.
const skipToken = "/skip"

// CLI is a line-based Prompter: it renders each question to out and reads one
// line of input per question from in. An explicit "/skip" line declines a
// question; an empty line is a valid free-text answer. EOF or a read error
// before a question is answered cancels the whole prompt.
type CLI struct {
	in  *bufio.Reader
	out io.Writer
}

// NewCLI returns a CLI prompter reading from in and writing prompts to out.
func NewCLI(in io.Reader, out io.Writer) *CLI {
	return &CLI{in: bufio.NewReader(in), out: out}
}

// Prompt asks each question in order.
func (c *CLI) Prompt(_ context.Context, req Request) (Response, error) {
	resp := Response{Answers: make([]Answer, len(req.Questions))}
	for i, q := range req.Questions {
		resp.Answers[i].Header = q.Header
	}
	for i, q := range req.Questions {
		c.render(q)
		line, err := c.in.ReadString('\n')
		if err != nil && err != io.EOF {
			return resp, err
		}
		if err == io.EOF && line == "" {
			// Nothing read (EOF) before this question: cancel everything.
			for j := range resp.Answers {
				resp.Answers[j].Status = StatusCancelled
				resp.Answers[j].Values = nil
			}
			return resp, nil
		}
		line = strings.TrimRight(line, "\r\n")
		if line == skipToken {
			resp.Answers[i].Status = StatusDeclined
			continue
		}
		resp.Answers[i].Status = StatusAnswered
		resp.Answers[i].Values = parseAnswer(q, line)
	}
	return resp, nil
}

func (c *CLI) render(q Question) {
	fmt.Fprintf(c.out, "%s: %s\n", q.Header, q.Text)
	for i, opt := range q.Options {
		fmt.Fprintf(c.out, "  %d) %s\n", i+1, opt)
	}
	switch {
	case len(q.Options) == 0:
		fmt.Fprintf(c.out, "Your answer (%s to skip): ", skipToken)
	case q.MultiSelect:
		fmt.Fprintf(c.out, "Enter number(s), comma-separated, or type a custom answer (%s to skip): ", skipToken)
	default:
		fmt.Fprintf(c.out, "Enter a number or type a custom answer (%s to skip): ", skipToken)
	}
}

// parseAnswer maps a line to answer values. With no options the line is a
// free-text answer. With options, comma-separated 1-based indices select
// those options; anything that is not a clean set of valid indices is treated
// as a custom free-typed answer. Single-select keeps only the first value.
func parseAnswer(q Question, line string) []string {
	if len(q.Options) == 0 {
		return []string{line}
	}
	parts := strings.Split(line, ",")
	vals := make([]string, 0, len(parts))
	for _, p := range parts {
		n, err := strconv.Atoi(strings.TrimSpace(p))
		if err != nil || n < 1 || n > len(q.Options) {
			return []string{line} // custom typed answer
		}
		vals = append(vals, q.Options[n-1])
	}
	if !q.MultiSelect && len(vals) > 1 {
		vals = vals[:1]
	}
	return vals
}
