// Command task-lifecycle-live is the live-LLM counterpart to the deterministic
// examples/task-lifecycle program. It wires the same Gantry task stack — agent,
// create_task/spawn_session server tools, the ask_user client tool, a critic
// verifier, in-memory stores, the driver/manager, and the real Dispatcher — but
// drives it with live Ollama clients instead of scripted mocks.
//
// Because a live model freewheels, this example observes and prints whatever the
// model actually does rather than asserting a fixed narrative. It ships no
// automated test; examples/task-lifecycle remains the deterministic, tested
// artifact.
package main
