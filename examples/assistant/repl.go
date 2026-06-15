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
//
// in is wrapped in a bufio.Reader. When the caller already holds a
// *bufio.Reader over the same stdin (as main does, sharing it with the
// confirmer and ask prompter), bufio.NewReader returns that same reader —
// so a single buffer serves the whole program. This matters: a separate
// reader here would read-ahead and swallow the y/N lines the confirmer
// needs, denying every mutating action under piped input.
func runREPL(ctx context.Context, mgr *session.Manager, sessionID string, in io.Reader, out io.Writer) error {
	reader := bufio.NewReader(in)

	resetCounter := 0
	fmt.Fprintf(out, "assistant ready (session %q). Type /help for commands.\n", sessionID)
	fmt.Fprint(out, "> ")

	for {
		raw, err := reader.ReadString('\n')
		if raw == "" && err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return fmt.Errorf("assistant: read input: %w", err)
		}
		line := strings.TrimSpace(raw)

		switch {
		case line == "":
			fmt.Fprint(out, "> ")
		case line == "/exit":
			return nil
		case line == "/help":
			fmt.Fprintln(out, helpText)
			fmt.Fprint(out, "> ")
		case line == "/reset":
			resetCounter++
			sessionID = fmt.Sprintf("%s-reset-%d", sessionID, resetCounter)
			fmt.Fprintf(out, "started a new session %q.\n", sessionID)
			fmt.Fprint(out, "> ")
		default:
			state, runErr := mgr.Session(sessionID).Run(ctx, line)
			switch {
			case errors.Is(runErr, context.Canceled):
				fmt.Fprintln(out, "\n(turn cancelled)")
			case errors.Is(runErr, harness.ErrHumanAborted):
				fmt.Fprintln(out, "(action denied — turn aborted, nothing was changed)")
			case errors.Is(runErr, session.ErrSaveFailed):
				fmt.Fprintln(out, replyText(state))
				fmt.Fprintln(out, "(warning: this turn was not saved)")
			case runErr != nil:
				fmt.Fprintf(out, "error: %v\n", runErr)
			default:
				fmt.Fprintln(out, replyText(state))
			}
			fmt.Fprint(out, "> ")
		}

		// A final line without a trailing newline returns content plus io.EOF;
		// process it above, then stop on the next read.
		if errors.Is(err, io.EOF) {
			return nil
		}
	}
}
