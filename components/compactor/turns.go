package compactor

import (
	"strings"
	"unicode/utf8"

	"github.com/farazhassan/gantry"
)

// summaryPrefix starts every message SummarizeTurns writes; it is how steps
// recognise a summary.
const summaryPrefix = "[Summary of earlier conversation]\n"

// isSummary reports whether m is a summary written by SummarizeTurns.
func isSummary(m gantry.Message) bool {
	return m.Role == gantry.RoleUser && strings.HasPrefix(m.Content, summaryPrefix)
}

// span is the half-open message range [start, end).
type span struct{ start, end int }

// segment splits msgs into a preamble (messages before the first RoleUser
// message, possibly empty) and turns: each turn is a RoleUser message plus
// every following non-user message (assistant replies, tool results) up to the
// next RoleUser message. Steps that only remove whole turns, or rewrite
// Content in place, never separate a tool call from its result.
func segment(msgs []gantry.Message) (preamble span, turns []span) {
	i := 0
	for i < len(msgs) && msgs[i].Role != gantry.RoleUser {
		i++
	}
	preamble = span{0, i}
	for i < len(msgs) {
		start := i
		i++
		for i < len(msgs) && msgs[i].Role != gantry.RoleUser {
			i++
		}
		turns = append(turns, span{start, i})
	}
	return preamble, turns
}

// olderTurns returns the turns a step may touch: all but the newest keep.
func olderTurns(turns []span, keep int) []span {
	if len(turns) <= keep {
		return nil
	}
	return turns[:len(turns)-keep]
}

// cloneMessages returns a slice-level copy of msgs (the Compactor contract).
func cloneMessages(msgs []gantry.Message) []gantry.Message {
	out := make([]gantry.Message, len(msgs))
	copy(out, msgs)
	return out
}

// totalTokens sums b.Count over msgs.
func totalTokens(msgs []gantry.Message, b Budget) int {
	n := 0
	for _, m := range msgs {
		n += b.Count(m)
	}
	return n
}

// runeStartAtOrBefore returns the largest i <= n (clamped to len(s)) that
// starts a rune in s.
func runeStartAtOrBefore(s string, n int) int {
	if n >= len(s) {
		return len(s)
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return n
}

// runeStartAtOrAfter returns the smallest i >= n that starts a rune in s, or
// len(s).
func runeStartAtOrAfter(s string, n int) int {
	if n < 0 {
		n = 0
	}
	for n < len(s) && !utf8.RuneStart(s[n]) {
		n++
	}
	return n
}

// capBytes returns s cut to at most n bytes on a rune boundary, with "…"
// appended when it was cut.
func capBytes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:runeStartAtOrBefore(s, n)] + "…"
}
