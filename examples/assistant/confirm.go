package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/farazhassan/gantry/components/humanloop"
)

// readOnlyTools lists, per server namespace, the tool names that are
// side-effect-free and may run without confirmation. Any tool not listed
// (including unknown namespaces) is treated as mutating and prompts — a
// fail-safe default. Adding a server is a one-line addition here.
var readOnlyTools = map[string]map[string]bool{
	"fs": {
		"read_file":                true,
		"read_multiple_files":      true,
		"list_directory":           true,
		"directory_tree":           true,
		"search_files":             true,
		"get_file_info":            true,
		"list_allowed_directories": true,
	},
	"web":  {"fetch": true},
	"time": nil, // nil sentinel: all tools in this namespace are read-only
}

// alwaysReadOnly are non-namespaced built-in tools that never mutate state.
var alwaysReadOnly = map[string]bool{
	"ask_user": true,
}

// isReadOnly reports whether the namespaced tool name is safe to auto-allow.
func isReadOnly(name string) bool {
	if alwaysReadOnly[name] {
		return true
	}
	ns, tool, ok := strings.Cut(name, "__")
	if !ok {
		return false // un-namespaced and not a known built-in -> fail-safe
	}
	tools, known := readOnlyTools[ns]
	if !known {
		return false // unknown namespace -> fail-safe
	}
	if tools == nil {
		return true // whole namespace is read-only (e.g. time)
	}
	return tools[tool]
}

// cliConfirmer implements humanloop.HumanInLoop over stdin/stdout. Read-only
// tools are auto-approved; mutating tools print the call and read a y/n line.
type cliConfirmer struct {
	r   *bufio.Reader
	out io.Writer
}

func newCLIConfirmer(in io.Reader, out io.Writer) *cliConfirmer {
	return &cliConfirmer{r: bufio.NewReader(in), out: out}
}

var _ humanloop.HumanInLoop = (*cliConfirmer)(nil)

func (c *cliConfirmer) Confirm(_ context.Context, a humanloop.Action) (humanloop.Decision, error) {
	if isReadOnly(a.Name) {
		return humanloop.Decision{Approved: true}, nil
	}
	fmt.Fprintf(c.out, "\nConfirm action: %s\n", a.Name)
	fmt.Fprintf(c.out, "  args: %s\n", prettyArgs(a.Args))
	fmt.Fprint(c.out, "Allow? [y/N]: ")

	line, err := c.r.ReadString('\n')
	if err != nil && line == "" {
		// EOF or read error with no input: fail safe (deny).
		return humanloop.Decision{Approved: false, Reason: "no confirmation input"}, nil
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return humanloop.Decision{Approved: true}, nil
	default:
		return humanloop.Decision{Approved: false, Reason: "denied by operator"}, nil
	}
}

// prettyArgs renders the tool input for display. Args is the tool call's
// json.RawMessage input; fall back to the raw form if it is not valid JSON.
func prettyArgs(args any) string {
	raw, ok := args.(json.RawMessage)
	if !ok {
		return fmt.Sprintf("%v", args)
	}
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, raw, "  ", "  "); err != nil {
		return string(raw)
	}
	return pretty.String()
}
