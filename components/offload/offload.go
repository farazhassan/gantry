package offload

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/farazhassan/gantry"
)

// State.Meta keys the middleware sets before each LLM call.
const (
	MetaSavedChars = "offload.saved_chars" // bytes currently replaced by previews
	MetaPreviews   = "offload.previews"    // tool results currently shown as previews
)

const middlewareName = "offload"

// Options configures New. Scope is required.
type Options struct {
	// Scope names the store namespace for ctx's conversation, e.g. a chat
	// id. An empty scope disables shrinking for that call.
	Scope func(ctx context.Context) string
	// MinChars: results this long (bytes) or shorter are never shrunk
	// (default 4000).
	MinChars int
	// AfterCalls: how many assistant messages must follow a result before it
	// is shrunk (default 1).
	AfterCalls int
	// PreviewChars is the preview budget in bytes (default 1500).
	PreviewChars int
	// KeepKeys are kept whole in previews (default DefaultKeepKeys).
	KeepKeys []string
	// OnError, if set, hears store failures. The result then stays full.
	OnError func(ctx context.Context, err error)
}

func (o Options) withDefaults() Options {
	if o.MinChars <= 0 {
		o.MinChars = 4000
	}
	if o.AfterCalls <= 0 {
		o.AfterCalls = 1
	}
	if o.PreviewChars <= 0 {
		o.PreviewChars = 1500
	}
	if o.KeepKeys == nil {
		o.KeepKeys = DefaultKeepKeys
	}
	return o
}

type component struct {
	store Store
	opts  Options
	keep  map[string]bool
}

// New returns the component that shrinks large, already-read tool results
// before each LLM call. It panics without a store or Options.Scope.
func New(store Store, opts Options) gantry.Component {
	if store == nil || opts.Scope == nil {
		panic("offload: New requires a Store and Options.Scope")
	}
	opts = opts.withDefaults()
	return &component{store: store, opts: opts, keep: keepSet(opts.KeepKeys)}
}

func (c *component) Install(a *gantry.Agent) error {
	return a.UseNamed(gantry.PhaseAssembleContext, middlewareName, func(next gantry.Handler) gantry.Handler {
		return func(ctx context.Context, s *gantry.State) error {
			// Let inner context assembly populate Messages first.
			if err := next(ctx, s); err != nil {
				return err
			}
			c.shrink(ctx, s)
			return nil
		}
	})
}

func (c *component) shrink(ctx context.Context, s *gantry.State) {
	if scope := c.opts.Scope(ctx); scope != "" {
		var msgs []gantry.Message // copied on first write: never alias the caller's array
		after := 0
		for i := len(s.Messages) - 1; i >= 0; i-- {
			m := s.Messages[i]
			if m.Role == gantry.RoleAssistant {
				after++
				continue
			}
			if m.Role != gantry.RoleTool || m.IsError || after < c.opts.AfterCalls || len(m.Content) <= c.opts.MinChars {
				continue
			}
			if _, ok := previewMeta(m.Content); ok {
				continue
			}
			p, err := c.offload(ctx, scope, m.Content)
			if err != nil {
				if c.opts.OnError != nil {
					c.opts.OnError(ctx, err)
				}
				continue
			}
			if len(p) >= len(m.Content) {
				continue
			}
			if msgs == nil {
				msgs = slices.Clone(s.Messages)
			}
			msgs[i].Content = p
		}
		if msgs != nil {
			s.Messages = msgs
			s.ContextUsage = gantry.ContextUsage{} // the measured anchor no longer describes Messages
		}
	}
	c.meter(s)
}

func (c *component) offload(ctx context.Context, scope, content string) (string, error) {
	ref := refFor(content)
	root, err := parseJSON([]byte(content))
	if err != nil {
		root = nil // not JSON: preview its head
	}
	if err := c.store.Put(ctx, storeKey(scope, ref), content); err != nil {
		return "", fmt.Errorf("offload: store %s: %w", ref, err)
	}
	if root != nil {
		// The index is first-writer-wins (Put never overwrites): if the same
		// src handle appears in two different results, the first one stays.
		for _, sp := range srcPaths(root) {
			entry, _ := json.Marshal(srcEntry{Ref: ref, Path: sp.path})
			if err := c.store.Put(ctx, storeKey(scope, "src:"+sp.src), string(entry)); err != nil {
				return "", fmt.Errorf("offload: index %s: %w", sp.src, err)
			}
		}
	}
	return buildPreview(root, content, ref, c.opts.PreviewChars, c.keep), nil
}

func (c *component) meter(s *gantry.State) {
	saved, previews := 0, 0
	for _, m := range s.Messages {
		if m.Role != gantry.RoleTool {
			continue
		}
		if meta, ok := previewMeta(m.Content); ok {
			saved += meta.Chars - len(m.Content)
			previews++
		}
	}
	if s.Meta == nil {
		s.Meta = map[string]any{}
	}
	s.Meta[MetaSavedChars] = saved
	s.Meta[MetaPreviews] = previews
}

// Resolve returns the full result behind content if content is a preview,
// otherwise content itself. On error it returns content unchanged.
func Resolve(ctx context.Context, store Store, scope, content string) (string, error) {
	meta, ok := previewMeta(content)
	if !ok {
		return content, nil
	}
	full, found, err := store.Get(ctx, storeKey(scope, meta.Ref))
	if err != nil {
		return content, fmt.Errorf("offload: resolve %s: %w", meta.Ref, err)
	}
	if !found {
		return content, fmt.Errorf("offload: %s is not in the store", meta.Ref)
	}
	return full, nil
}
