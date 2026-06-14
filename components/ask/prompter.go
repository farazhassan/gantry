// components/ask/prompter.go
package ask

import "context"

// Question is a single question posed to the human.
type Question struct {
	Header      string   `json:"header"`                // short label, <= 12 chars; identifies the answer
	Text        string   `json:"text"`                  // the question text
	Options     []string `json:"options,omitempty"`     // optional; 2-4 if present; empty => free-text answer
	MultiSelect bool     `json:"multiSelect,omitempty"` // allow selecting multiple options
}

// Request is a batch of 1-4 questions posed together.
type Request struct {
	Questions []Question `json:"questions"`
}

// Status is the per-question outcome.
type Status string

const (
	StatusAnswered  Status = "answered"  // user provided an answer
	StatusDeclined  Status = "declined"  // user skipped this question
	StatusCancelled Status = "cancelled" // user abandoned the whole prompt
)

// Answer is the human's reply to one Question.
type Answer struct {
	Header string   `json:"header"`           // echoes Question.Header for correlation
	Status Status   `json:"status"`           // answered / declined / cancelled
	Values []string `json:"values,omitempty"` // selected option(s) and/or free-typed text; nil if not answered
}

// Response carries one Answer per Question, in the same order.
type Response struct {
	Answers []Answer `json:"answers"`
}

// Prompter surfaces a Request to the human and returns their Response. The
// error return is for transport failures only (e.g. a closed input stream);
// human outcomes are carried by Answer.Status.
type Prompter interface {
	Prompt(ctx context.Context, req Request) (Response, error)
}

// PrompterFunc adapts a function to the Prompter interface.
type PrompterFunc func(ctx context.Context, req Request) (Response, error)

// Prompt calls f.
func (f PrompterFunc) Prompt(ctx context.Context, req Request) (Response, error) {
	return f(ctx, req)
}
