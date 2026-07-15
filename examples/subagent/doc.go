// Package main runs a coordinator agent with two specialist sub-agents wired
// in as delegate tools: a researcher and a writer. It shows subagent.New (the
// agent-as-tool primitive), subagent.Component (tool wiring plus child-usage
// folding into the coordinator's State.Usage), and context isolation: each
// specialist sees only the goal and briefing it is handed, never the
// coordinator's transcript. Scripted MockLLMClients keep it hermetic.
//
// Run with:
//
//	go run ./examples/subagent
package main
