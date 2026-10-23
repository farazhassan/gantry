package tool

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/farazhassan/gantry"
)

// InvalidJSONContent is the tool result content the model sees for a call
// whose arguments are not valid JSON. The call never runs (server tools) or
// is never surfaced to the client (client tools).
const InvalidJSONContent = "invalid tool arguments: not valid JSON"

// ArgsValidator checks a tool call's raw arguments before the call runs or
// is surfaced to a client. A non-nil error rejects the call; its message
// becomes the tool result content the model sees.
type ArgsValidator func(input json.RawMessage) error

// ValidateArgs reports whether input is acceptable as tool-call arguments:
// empty input (a no-argument call) or valid JSON. Any other input returns an
// error matching gantry.ErrToolInvalidArgs whose message is
// InvalidJSONContent.
//
// Registry.Invoke and the client-tool middleware (Client, DynamicClient)
// apply it to every call, so a model that emits malformed arguments — e.g.
// an unescaped quote inside a string — gets an error result and a chance to
// retry instead of a tool fed bytes it cannot parse.
func ValidateArgs(input json.RawMessage) error {
	if len(input) == 0 || json.Valid(input) {
		return nil
	}
	return InvalidArgsError(errors.New(InvalidJSONContent))
}

// InvalidArgsError marks err as a gantry.ErrToolInvalidArgs (matchable via
// errors.Is alongside err itself) without changing its message, which is
// what the model sees. Validators use it so callers can tell a rejected call
// from one that ran and failed. A nil err returns nil.
func InvalidArgsError(err error) error {
	if err == nil || errors.Is(err, gantry.ErrToolInvalidArgs) {
		return err
	}
	return invalidArgsError{err}
}

type invalidArgsError struct{ err error }

func (e invalidArgsError) Error() string   { return e.err.Error() }
func (e invalidArgsError) Unwrap() []error { return []error{gantry.ErrToolInvalidArgs, e.err} }

// rejectClientCalls runs check over every pending client-side call (as
// marked by Client or DynamicClient this run) and, for each it rejects,
// drops the call from s.PendingToolCalls and appends an error ToolResult in
// its place. The model sees that result on its next turn; the call is never
// surfaced to the client. If every client call is rejected, the suspend
// middleware has nothing left to suspend on and the run continues.
func rejectClientCalls(s *gantry.State, check func(gantry.ToolCall) error) {
	set := clientToolSet(s)
	if len(set) == 0 {
		return
	}
	kept := s.PendingToolCalls[:0]
	for _, cl := range s.PendingToolCalls {
		if set[cl.Name] {
			if err := check(cl); err != nil {
				err = InvalidArgsError(err)
				s.ToolResults = append(s.ToolResults, gantry.ToolResult{
					CallID:  cl.ID,
					Content: err.Error(),
					IsError: true,
					Err:     err,
				})
				continue
			}
		}
		kept = append(kept, cl)
	}
	s.PendingToolCalls = kept
}

const clientValidatePrefix = "components/tool:client_validate:"

type clientArgsValidatorComponent struct {
	name string
	v    ArgsValidator
}

// ClientArgsValidator returns a Component that checks the arguments of
// client-side calls to the tool named name with v before they are surfaced
// to the client. It applies to calls marked client-side by either Client or
// DynamicClient — a DynamicClient tool's def comes from the request, so the
// validator is registered here, on the agent, rather than on the def. A
// rejected call becomes an error ToolResult (matching
// gantry.ErrToolInvalidArgs) fed back to the model instead of suspending the
// run on a call the client cannot render. Server-side calls with the same
// name are not affected (a server tool validates its own input in Invoke).
//
// Installing two validators for the same name on one agent, an empty name,
// or a nil v returns an error.
func ClientArgsValidator(name string, v ArgsValidator) gantry.Component {
	return &clientArgsValidatorComponent{name: name, v: v}
}

func (c *clientArgsValidatorComponent) Install(a *gantry.Agent) error {
	if c.name == "" {
		return errors.New("tool: ClientArgsValidator requires a non-empty tool name")
	}
	if c.v == nil {
		return errors.New("tool: ClientArgsValidator requires a non-nil validator for " + c.name)
	}
	mwName := clientValidatePrefix + c.name
	for _, n := range a.MiddlewareNames(gantry.PhaseToolExec) {
		if n == mwName {
			return errors.New("tool: client args validator already installed for " + c.name)
		}
	}
	return a.UseNamed(gantry.PhaseToolExec, mwName, func(next gantry.Handler) gantry.Handler {
		return func(ctx context.Context, s *gantry.State) error {
			rejectClientCalls(s, func(cl gantry.ToolCall) error {
				if cl.Name != c.name {
					return nil
				}
				// Malformed JSON gets the uniform message whichever
				// PhaseToolExec middleware sees the call first.
				if err := ValidateArgs(cl.Input); err != nil {
					return err
				}
				return c.v(cl.Input)
			})
			return next(ctx, s)
		}
	})
}
