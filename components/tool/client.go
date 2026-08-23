package tool

import (
	"context"
	"errors"
	"strings"

	"github.com/farazhassan/gantry"
)

// Middleware names installed by Client and DynamicClient.
const (
	advertiseClientName        = "components/tool:client_advertise"
	dynamicClientAdvertiseName = "components/tool:dynamic_client_advertise"
	suspendName                = "components/tool:client_suspend"

	// clientToolsMetaKey carries the set of client-side tool names from
	// whichever PhaseStart middleware advertised them (Client's or
	// DynamicClient's) to the PhaseToolExec dispatch and the PhaseObserve
	// suspend middleware. Namespaced per the State.Meta convention.
	clientToolsMetaKey = "components/tool:client_tools"

	// pendingClientDefsMetaKey carries tool defs set via
	// SetPendingClientTools into DynamicClient's PhaseStart middleware. A
	// separate key from clientToolsMetaKey: it must survive into PhaseStart,
	// where state.Tools is rebuilt every call (Agent.run resets state.Tools
	// to nil before running PhaseStart on every Run/RunFrom/RunStream/
	// RunFromStream/Resume/ResumeStream call), so state.Tools itself cannot
	// be used to pass defs into a run — only state.Meta survives that reset.
	pendingClientDefsMetaKey = "components/tool:pending_client_defs"
)

// validateClientDefNames checks defs for the constraints every client-tool
// name set must satisfy — non-empty, non-duplicate — returning the name set
// on success. Shared by Client.Install (validating its fixed defs once, at
// agent-construction time) and SetPendingClientTools (validating its defs
// every call, at per-run time).
func validateClientDefNames(defs []gantry.ToolDef) (map[string]bool, error) {
	names := make(map[string]bool, len(defs))
	for _, d := range defs {
		if d.Name == "" {
			return nil, errors.New("tool: client tools require non-empty tool names")
		}
		if names[d.Name] {
			return nil, errors.New("tool: duplicate client tool name " + d.Name)
		}
		names[d.Name] = true
	}
	return names, nil
}

type clientComponent struct{ defs []gantry.ToolDef }

// Client returns a Component declaring definition-only "client-side" tools,
// fixed for the agent's lifetime. Their ToolDefs are advertised to the LLM on
// every run, but they have no server-side Invoke: when the model calls one,
// the run suspends at the observe boundary with state.DoneReason ==
// gantry.DoneClientToolCall and the call(s) left in state.PendingToolCalls.
// Installing client tools twice on the same agent, an empty tool name, a
// duplicate name, or installing alongside DynamicClient returns an error.
//
// Use DynamicClient instead when the tool set varies per caller/session
// (e.g. an AG-UI handler wrapping CopilotKit frontend actions) rather
// than being fixed at agent construction.
func Client(defs ...gantry.ToolDef) gantry.Component {
	return &clientComponent{defs: defs}
}

func (c *clientComponent) Install(a *gantry.Agent) error {
	names, err := validateClientDefNames(c.defs)
	if err != nil {
		return err
	}
	if DynamicClientInstalled(a) {
		return errors.New("tool: Client cannot be installed alongside DynamicClient")
	}
	defsCopy := append([]gantry.ToolDef(nil), c.defs...)

	// SuspendClientCalls is also auto-installed by registryComponent
	// (tool.New/FromTools) when a bare agent needs suspend detection for a
	// ResumableTool with no Client/DynamicClient present — check first so
	// installing Client after that composes instead of colliding on the
	// shared middleware name. Mutual exclusion with DynamicClient is
	// enforced explicitly above, not by this collision, since both of those
	// would otherwise also be safely order-tolerant here.
	if !SuspendClientCallsInstalled(a) {
		if err := SuspendClientCalls().Install(a); err != nil {
			return err
		}
	}

	// PhaseStart: advertise the fixed defs to the LLM and mark their names as
	// client-side, every run — state.Tools and state.Meta[clientToolsMetaKey]
	// are per-run scratch (see pendingClientDefsMetaKey's doc comment above).
	return a.UseNamed(gantry.PhaseStart, advertiseClientName, func(next gantry.Handler) gantry.Handler {
		return func(ctx context.Context, s *gantry.State) error {
			s.Tools = append(s.Tools, defsCopy...)
			markClientNames(s, names)
			return next(ctx, s)
		}
	})
}

// SetPendingClientTools stashes tool defs to be advertised and treated as
// client-side for the very next Run/RunFrom/RunStream/RunFromStream/Resume/
// ResumeStream call on s — and no further, since state.Tools is rebuilt from
// scratch on every such call (see pendingClientDefsMetaKey's doc comment).
// Call this on the State you are about to pass into that call. Requires
// DynamicClient installed on the agent to take effect; use
// DynamicClientInstalled to verify that ahead of time and fail clearly
// instead of silently losing the call.
//
// An empty tool name, or a duplicate name among the defs passed in this
// call, returns an error and leaves s unmodified — the same validation
// Client.Install performs at agent-construction time, applied here at
// per-run time since a caller decoding tool names from a request (e.g. an
// AG-UI handler) can hand this malformed input just as easily.
//
// Unlike Client (fixed defs at agent-construction time), this supports a
// tool set that varies per caller/session — e.g. an AG-UI handler calling it
// from its request-decoding path with tools decoded from a request (like a
// CopilotKit frontend action) — at the cost of needing to be called before
// every run/resume that should advertise them.
func SetPendingClientTools(s *gantry.State, defs ...gantry.ToolDef) error {
	if len(defs) == 0 {
		return nil
	}
	if _, err := validateClientDefNames(defs); err != nil {
		return err
	}
	if s.Meta == nil {
		s.Meta = map[string]any{}
	}
	existing, _ := s.Meta[pendingClientDefsMetaKey].([]gantry.ToolDef)
	s.Meta[pendingClientDefsMetaKey] = append(existing, defs...)
	return nil
}

type dynamicClientComponent struct{}

// DynamicClient returns a Component that advertises and suspends on per-run
// client tool defs set via SetPendingClientTools, instead of a fixed list
// installed once. Install this at agent construction when the tool set is
// only known per-request. Installing it twice, or alongside Client, on the
// same agent returns an error.
func DynamicClient() gantry.Component {
	return &dynamicClientComponent{}
}

func (c *dynamicClientComponent) Install(a *gantry.Agent) error {
	if clientInstalled(a) {
		return errors.New("tool: DynamicClient cannot be installed alongside Client")
	}
	// See the matching comment in clientComponent.Install: check first so
	// this composes with registryComponent's own auto-install instead of
	// colliding on the shared middleware name.
	if !SuspendClientCallsInstalled(a) {
		if err := SuspendClientCalls().Install(a); err != nil {
			return err
		}
	}
	return a.UseNamed(gantry.PhaseStart, dynamicClientAdvertiseName, func(next gantry.Handler) gantry.Handler {
		return func(ctx context.Context, s *gantry.State) error {
			defs, _ := s.Meta[pendingClientDefsMetaKey].([]gantry.ToolDef)
			names := make(map[string]bool, len(defs))
			if len(defs) > 0 {
				// Consume the pending defs: delete them from Meta so they
				// don't silently re-advertise on a later Run/Resume call on
				// the same *State that forgot to call
				// SetPendingClientTools again (see its doc comment).
				delete(s.Meta, pendingClientDefsMetaKey)
				s.Tools = append(s.Tools, defs...)
				for _, d := range defs {
					names[d.Name] = true
				}
			}
			// Unlike Client's fixed name set (safe to merge every call via
			// markClientNames, since it's the same names every time),
			// DynamicClient's name set varies per run and must be replaced
			// wholesale — including being cleared when this run set no
			// pending defs — so a name marked client-side on an earlier
			// run/resume of the same *State can't linger and wrongly
			// re-suspend a call nobody advertised this turn (see
			// replaceClientNames, and
			// TestDynamicClientStaleNameDoesNotResuspendOnLaterResume).
			replaceClientNames(s, names)
			return next(ctx, s)
		}
	})
}

// SuspendClientCalls installs the PhaseObserve middleware that suspends a
// run when any pending tool call's name was marked client-side this run (by
// Client's or DynamicClient's PhaseStart middleware). Client and
// DynamicClient both install this internally — most callers want one of
// those, not this directly. Installing it twice on the same agent returns
// an error.
func SuspendClientCalls() gantry.Component {
	return suspendComponent{}
}

type suspendComponent struct{}

func (suspendComponent) Install(a *gantry.Agent) error {
	return a.UseNamed(gantry.PhaseObserve, suspendName, func(next gantry.Handler) gantry.Handler {
		return func(ctx context.Context, s *gantry.State) error {
			set := clientToolSet(s)
			var clientCalls []gantry.ToolCall
			// badClientResults collects a synthetic error ToolResult for each
			// declared client-tool call whose own ID already contains
			// pendingIDSep. A declared client-tool call is never invoked
			// server-side (that's the point of it), so it produces no
			// ToolResult of its own to fold an error into the way the
			// r.CallID check below does for a ResumableTool's origin — this
			// call ID is this level's own freshly-dispatched LLM tool_call
			// ID, never itself composite, so (like r.CallID, and unlike a
			// Pending[i].ID) it is safe to reject centrally here: nothing
			// about legitimate nesting ever produces one containing the
			// separator. See pendingIDSepContractErr's doc comment for why
			// the same reasoning does not extend to Pending[i].ID.
			var badClientResults []gantry.ToolResult
			for _, cl := range s.PendingToolCalls {
				if !set[cl.Name] {
					continue
				}
				if strings.Contains(cl.ID, pendingIDSep) {
					err := pendingIDSepClientCallErr(cl.Name, cl.ID)
					badClientResults = append(badClientResults, gantry.ToolResult{
						CallID:  cl.ID,
						Content: err.Error(),
						IsError: true,
						Err:     err,
					})
					continue
				}
				clientCalls = append(clientCalls, cl)
			}

			// Pull out any tool-pending results (a *gantry.PendingResult
			// surfaced via ToolResult.Err — see components/tool's dispatch)
			// before next folds ToolResults into the transcript, so a
			// pending call is never mistaken for a finished one.
			entries := map[string]pendingEntry{}
			var toolPending []gantry.ToolCall
			kept := s.ToolResults[:0]
			for _, r := range s.ToolResults {
				var pending *gantry.PendingResult
				// errors.As can match and still leave pending nil: a tool that
				// mistakenly does
				// `var pr *gantry.PendingResult; return out, pr` returns a
				// non-nil error interface wrapping a nil pointer.
				// Treating that as a genuine suspend signal would panic
				// below on pending.Resume — pending != nil routes it to
				// the fallthrough below instead, which passes r through
				// unchanged as a normal, already-resolved ToolResult.
				if errors.As(r.Err, &pending) && pending != nil {
					toolName := toolNameFor(s.PendingToolCalls, r.CallID)

					// r.CallID already containing pendingIDSep would corrupt
					// the composite ID (origin + pendingIDSep + leaf) built
					// below: splitPendingID only ever splits at the FIRST
					// separator, so the entries map (keyed by the real,
					// full r.CallID) would become unreachable from the
					// surfaced composite ID, whose recovered "origin" is
					// only the prefix up to that first separator — a
					// genuine mismatch, not a false positive. r.CallID is
					// always this level's own freshly-dispatched LLM
					// tool_call ID (never itself composite — composite IDs
					// are only ever introduced by this very construction),
					// so this check cannot fire on legitimate nesting.
					//
					// A Pending[i].ID containing pendingIDSep, by contrast,
					// is deliberately NOT checked here: multi-level nesting
					// (an agent-as-tool delegate whose own child already
					// suspended) legitimately passes an already-composite
					// child ID straight through as Pending[i].ID — see
					// splitPendingID's doc comment ("leaf ... may itself
					// still be composite, for more than one level of
					// nesting") and subagent's delegateTool.asResult, which
					// does exactly that. Each level only ever splits once,
					// forwarding its own residual leaf opaquely to the next
					// level down, so an embedded separator there causes no
					// misparse; rejecting it would break that design.
					if strings.Contains(r.CallID, pendingIDSep) {
						err := pendingIDSepContractErr(toolName, r.CallID)
						kept = append(kept, gantry.ToolResult{
							CallID:  r.CallID,
							Content: err.Error(),
							IsError: true,
							Err:     err,
						})
						continue
					}

					entries[r.CallID] = pendingEntry{
						ToolName: toolName,
						Resume:   pending.Resume,
					}
					for _, p := range pending.Pending {
						toolPending = append(toolPending, gantry.ToolCall{
							ID:    r.CallID + pendingIDSep + p.ID,
							Name:  p.Name,
							Input: p.Input,
						})
					}
					continue
				}
				kept = append(kept, r)
			}
			s.ToolResults = append(kept, badClientResults...)

			if err := next(ctx, s); err != nil {
				return err
			}

			if len(clientCalls) > 0 || len(toolPending) > 0 {
				s.PendingToolCalls = append(append(s.PendingToolCalls[:0], clientCalls...), toolPending...)
				s.Done = true
				s.DoneReason = gantry.DoneClientToolCall
			}
			setPendingEntries(s, entries)
			return nil
		}
	})
}

// SuspendClientCallsInstalled reports whether SuspendClientCalls is
// installed on a (directly, or via Client/DynamicClient). Callers that mark
// tool names dynamically per-run (e.g. an AG-UI handler) can check this
// once at setup and fail clearly instead of silently losing an unmarked
// run's client tool calls: without it, an unresolved pending call is simply
// cleared by DefaultObserveHandler with no result message, which the next
// LLM call sees as an unexplained gap in the transcript.
func SuspendClientCallsInstalled(a *gantry.Agent) bool {
	for _, name := range a.MiddlewareNames(gantry.PhaseObserve) {
		if name == suspendName {
			return true
		}
	}
	return false
}

// DynamicClientInstalled reports whether DynamicClient specifically (not
// just Client) is installed on a. Request-declared, per-run client tools
// (set via SetPendingClientTools) are only ever advertised to the LLM by
// DynamicClient's PhaseStart middleware — SuspendClientCallsInstalled alone
// does not guarantee that, since Client also installs the shared suspend
// middleware without reading per-run pending defs. Callers that decode
// dynamic per-request tool declarations (e.g. an AG-UI handler) should
// check this specifically before trusting they'll be advertised.
func DynamicClientInstalled(a *gantry.Agent) bool {
	for _, name := range a.MiddlewareNames(gantry.PhaseStart) {
		if name == dynamicClientAdvertiseName {
			return true
		}
	}
	return false
}

// clientInstalled reports whether Client specifically (not DynamicClient) is
// installed on a. Mirrors DynamicClientInstalled's shape, checking for
// advertiseClientName instead. Unexported: it exists only to give
// dynamicClientComponent.Install an explicit Client-vs-DynamicClient mutual
// exclusion check that's independent of the SuspendClientCalls
// already-registered collision (see the comments in both Install methods),
// so there's no need to expose it publicly the way DynamicClientInstalled is.
func clientInstalled(a *gantry.Agent) bool {
	for _, name := range a.MiddlewareNames(gantry.PhaseStart) {
		if name == advertiseClientName {
			return true
		}
	}
	return false
}

// markClientNames merges names into the client-tool-name set recorded on
// s.Meta, so multiple markers within the same PhaseStart chain compose
// instead of clobbering each other.
func markClientNames(s *gantry.State, names map[string]bool) {
	if s.Meta == nil {
		s.Meta = map[string]any{}
	}
	set, _ := s.Meta[clientToolsMetaKey].(map[string]bool)
	if set == nil {
		set = make(map[string]bool, len(names))
	}
	for n := range names {
		set[n] = true
	}
	s.Meta[clientToolsMetaKey] = set
}

// replaceClientNames overwrites the client-tool-name set recorded on s.Meta
// with exactly names, clearing the key entirely when names is empty. Used by
// DynamicClient, whose per-run tool set must not carry stale names forward
// from an earlier run/resume on the same *State — unlike Client's fixed set,
// which is the same every call and so is safe to merge via markClientNames.
func replaceClientNames(s *gantry.State, names map[string]bool) {
	if len(names) == 0 {
		if s.Meta != nil {
			delete(s.Meta, clientToolsMetaKey)
		}
		return
	}
	if s.Meta == nil {
		s.Meta = map[string]any{}
	}
	s.Meta[clientToolsMetaKey] = names
}

// clientToolSet returns the client-tool name set recorded by Client or
// DynamicClient for this run, or nil when no client tools were configured. A
// nil map is safe to index (always reports false), so callers need no
// special-casing.
func clientToolSet(s *gantry.State) map[string]bool {
	if s.Meta == nil {
		return nil
	}
	set, _ := s.Meta[clientToolsMetaKey].(map[string]bool)
	return set
}
