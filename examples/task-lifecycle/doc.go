// Command task-lifecycle is a runnable example of the full Gantry task
// lifecycle, driven entirely by scripted mock LLMs (no live API calls).
//
// It tells one story — drafting a release announcement — that touches every
// stage of the task layer:
//
//   - the status machine: pending -> active -> awaiting_input -> done;
//   - the critic gate: a first draft is rejected, revised, then accepted;
//   - same-session spawning (create_task): a proofread follow-on runs after the
//     draft via the session's FIFO queue;
//   - cross-session spawning (spawn_session): an unrelated "schedule posts"
//     session is created detached and enqueued on the ReadyQueue;
//   - headless driving: a real Dispatcher drains the ReadyQueue and drives the
//     detached session with no human attached until its agent asks a question
//     (ask_user), parking the task at awaiting_input and firing the
//     WithNotifier callback.
//
// RunExample returns the observable milestones; main prints them. In production
// the two mock LLM clients are replaced with live clients with no other change.
package main
