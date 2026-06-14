// Package ask provides an LLM-initiated tool that asks the human operator one
// to four structured questions and returns their answers to the model.
//
// The ask_user tool (NewTool) is an ordinary tool.Tool and needs no harness
// changes. It delegates the actual human interaction to a Prompter — the
// elicitation analogue of humanloop.HumanInLoop — so callers choose how the
// question reaches the human (CLI, web UI, test double). Unlike a humanloop
// approval gate, an ask never aborts the run: every outcome (answered,
// declined, cancelled) flows back to the model, which decides what to do next.
package ask
