package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/farazhassan/gantry/harness"
	"github.com/farazhassan/gantry/session"
)

const helpText = `Commands:
  /help        show this help
  /reset       start a fresh conversation (new session)
  /exit        quit the assistant
Anything else is sent to the assistant as a message.`

// runREPL reads lines from in, runs each as one assistant turn under the
// current session, and writes replies to out. It returns nil on /exit or
// when in reaches EOF. The context cancels in-flight turns (wired to a
// signal in main); a cancelled turn is reported and the loop continues.
func runREPL(ctx context.Context, mgr *session.Manager, sessionID string, in io.Reader, out io.Writer) error {
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	resetCounter := 0
	fmt.Fprintf(out, "assistant ready (session %q). Type /help for commands.\n", sessionID)
	fmt.Fprint(out, "> ")

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		switch {
		case line == "":
			fmt.Fprint(out, "> ")
			continue
		case line == "/exit":
			return nil
		case line == "/help":
			fmt.Fprintln(out, helpText)
			fmt.Fprint(out, "> ")
			continue
		case line == "/reset":
			resetCounter++
			sessionID = fmt.Sprintf("%s-reset-%d", sessionID, resetCounter)
			fmt.Fprintf(out, "started a new session %q.\n", sessionID)
			fmt.Fprint(out, "> ")
			continue
		}

		state, err := mgr.Session(sessionID).Run(ctx, line)
		switch {
		case errors.Is(err, context.Canceled):
			fmt.Fprintln(out, "\n(turn cancelled)")
		case errors.Is(err, harness.ErrHumanAborted):
			fmt.Fprintln(out, "(action denied — turn aborted, nothing was changed)")
		case errors.Is(err, session.ErrSaveFailed):
			fmt.Fprintln(out, replyText(state))
			fmt.Fprintln(out, "(warning: this turn was not saved)")
		case err != nil:
			fmt.Fprintf(out, "error: %v\n", err)
		default:
			fmt.Fprintln(out, replyText(state))
		}
		fmt.Fprint(out, "> ")
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("assistant: read input: %w", err)
	}
	return nil // EOF
}
