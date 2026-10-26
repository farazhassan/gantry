package offload

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/farazhassan/gantry"
	"github.com/farazhassan/gantry/components/tool"
)

// ReadToolName is the read tool's name.
const ReadToolName = "read_result"

// MaxReadChars caps a read_result value (bytes); larger values come back previewed.
const MaxReadChars = 8000

// replySlack is how far the whole reply (ref, path, total, next_offset keys
// included) may exceed MaxReadChars.
const replySlack = 200

const defaultReadLimit = 20

const readDescription = "Read a stored tool result in full or in parts. Large tool results you " +
	"have already read are shown as a preview with an _offloaded block; pass its ref, or a " +
	"src handle such as mtr_…, to read the data behind it. path selects a field " +
	"(e.g. items[0].result.series[2]); offset and limit page through an array or an " +
	"object's fields. Replies are capped: when a value is shown as a preview, read a " +
	"narrower path or follow its read_result hints."

const readSchema = `{"type":"object","properties":{` +
	`"ref":{"type":"string","description":"A res_… ref from an _offloaded block, or a src handle."},` +
	`"path":{"type":"string","description":"Field path into the result, e.g. series[0].points. Empty for the whole result (relative to the handle's object for a src)."},` +
	`"offset":{"type":"integer","minimum":0,"description":"First array element or object field to return."},` +
	`"limit":{"type":"integer","minimum":1,"maximum":200,"description":"How many array elements or object fields to return (default 20)."}},` +
	`"required":["ref"],"additionalProperties":false}`

type readTool struct {
	store Store
	scope func(context.Context) string
}

// ReadTool returns the read_result tool over store, scoped like the
// component that wrote it.
func ReadTool(store Store, scope func(context.Context) string) tool.Tool {
	return readTool{store: store, scope: scope}
}

func (readTool) Definition() gantry.ToolDef {
	return gantry.ToolDef{Name: ReadToolName, Description: readDescription, Schema: json.RawMessage(readSchema)}
}

func (t readTool) Invoke(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
	var in struct {
		Ref    string `json:"ref"`
		Path   string `json:"path"`
		Offset int    `json:"offset"`
		Limit  int    `json:"limit"`
	}
	if err := json.Unmarshal(input, &in); err != nil || in.Ref == "" {
		return nil, errors.New("read_result: ref is required")
	}
	in.Offset = max(in.Offset, 0)
	in.Limit = max(in.Limit, 0)
	scope := t.scope(ctx)
	if scope == "" {
		return nil, errors.New("read_result: no stored results in this conversation")
	}
	shown := cutBytes(in.Ref, 200)
	var content, base string
	var found bool
	if strings.HasPrefix(in.Ref, "res_") {
		v, ok, err := t.store.Get(ctx, storeKey(scope, in.Ref))
		if err != nil {
			return nil, fmt.Errorf("read_result: %w", err)
		}
		content, found = v, ok
	}
	if !found { // a src handle, or a res_ ref that is really one
		raw, ok, err := t.store.Get(ctx, storeKey(scope, "src:"+in.Ref))
		if err != nil {
			return nil, fmt.Errorf("read_result: %w", err)
		}
		if !ok {
			if strings.HasPrefix(in.Ref, "res_") {
				return nil, fmt.Errorf("read_result: %s has expired or never existed; call the original tool again", shown)
			}
			return nil, fmt.Errorf("read_result: no stored result holds %q; call the tool that produced it again", shown)
		}
		var e srcEntry
		if err := json.Unmarshal([]byte(raw), &e); err != nil {
			return nil, fmt.Errorf("read_result: bad index entry for %q", shown)
		}
		v, ok, err := t.store.Get(ctx, storeKey(scope, e.Ref))
		if err != nil {
			return nil, fmt.Errorf("read_result: %w", err)
		}
		if !ok {
			return nil, fmt.Errorf("read_result: %s has expired or never existed; call the original tool again", cutBytes(e.Ref, 200))
		}
		content, base = v, e.Path
	}
	// The reply echoes the ref as the caller gave it, so a src handle's
	// hints work when passed straight back.
	echo := in.Ref
	out := &node{kind: kindObject}
	add := func(k string, v *node) { out.keys = append(out.keys, k); out.vals = append(out.vals, v) }
	add("ref", textNode(echo))
	const replyCap = MaxReadChars + replySlack

	root, perr := parseJSON([]byte(content))
	if perr != nil {
		if in.Path != "" || base != "" {
			return nil, errors.New("read_result: this result is not JSON; omit path and page with offset")
		}
		return pageText(out, content, in.Offset, replyCap), nil
	}
	// For a src handle the object at base is the root of everything below:
	// the caller's path, the echoed path and every hint are relative to it.
	if base != "" {
		r, err := root.at(base)
		if err != nil {
			return nil, fmt.Errorf("read_result: %w", err)
		}
		root = r
	}
	path := in.Path
	n, err := root.at(path)
	if err != nil {
		return nil, fmt.Errorf("read_result: %w", err)
	}
	if len(path) <= 1000 {
		add("path", textNode(path))
	}
	frame := len(out.String()) + len(`,"value":,"total":0000000,"next_offset":0000000`)
	budget := max(replyCap-frame, 1000)
	p := previewer{budget: budget, keep: map[string]bool{}}

	limit := in.Limit
	if limit <= 0 {
		limit = defaultReadLimit
	}
	switch {
	case n.kind == kindScalar && len(n.raw) > budget && n.raw[0] == '"':
		if str, ok := n.str(); ok {
			return pageText(out, str, in.Offset, replyCap), nil
		}
		add("value", p.fit(n, path))
	case n.kind == kindArray:
		count := len(n.elems)
		start := min(in.Offset, count)
		sub := func(a, b int) *node { return &node{kind: kindArray, elems: n.elems[a:b]} }
		end := fitPage(start, min(start+limit, count), budget, func(a, b int) string { return sub(a, b).String() })
		page := sub(start, end)
		if len(page.String()) > budget { // one element is too big on its own
			page = &node{kind: kindArray, elems: []*node{p.fit(n.elems[start], indexPath(path, start))}}
		}
		add("value", page)
		add("total", intNode(count))
		if end < count {
			add("next_offset", intNode(end))
		}
	case n.kind == kindObject && (in.Offset > 0 || in.Limit > 0):
		// Fields are paged over ALL keys in document order, the same index
		// the preview's _more offset uses. Without offset or limit an object
		// is read whole, or fitted with hints (the default case).
		count := len(n.keys)
		start := min(in.Offset, count)
		sub := func(a, b int) *node {
			keys := make([]string, b-a)
			for i := range keys {
				keys[i] = cutBytes(n.keys[a+i], 200) // a huge key must not blow the cap
			}
			return &node{kind: kindObject, keys: keys, vals: n.vals[a:b]}
		}
		end := fitPage(start, min(start+limit, count), budget, func(a, b int) string { return sub(a, b).String() })
		page := sub(start, end)
		if len(page.String()) > budget { // one field is too big on its own
			page = &node{kind: kindObject, keys: []string{cutBytes(n.keys[start], 200)},
				vals: []*node{p.fit(n.vals[start], keyPath(path, n.keys[start]))}}
		}
		add("value", page)
		add("total", intNode(count))
		if end < count {
			add("next_offset", intNode(end))
		}
	default:
		if len(n.String()) > budget {
			n = p.fit(n, path)
		}
		add("value", n)
	}
	return json.RawMessage(out.String()), nil
}

// fitPage returns the largest end in (start, end0] such that enc(start, end)
// fits budget, halving the page until it does; a range of one element is
// left for the caller to fit. An empty range returns start.
func fitPage(start, end0, budget int, enc func(a, b int) string) int {
	end := end0
	for end > start+1 && len(enc(start, end)) > budget {
		end = start + (end-start)/2
	}
	return end
}

// pageText reads non-JSON content by byte offset. Cuts fall on rune
// boundaries, the encoded reply stays within replyCap, and next_offset is
// always past the page's start.
func pageText(out *node, content string, offset, replyCap int) json.RawMessage {
	start := min(offset, len(content))
	for i := 0; i < utf8.UTFMax-1 && start > 0 && start < len(content) && !utf8.RuneStart(content[start]); i++ {
		start--
	}
	build := func(end int) string {
		o := &node{kind: kindObject, keys: append([]string{}, out.keys...), vals: append([]*node{}, out.vals...)}
		o.keys = append(o.keys, "value", "total")
		o.vals = append(o.vals, textNode(content[start:end]), intNode(len(content)))
		if end < len(content) {
			o.keys = append(o.keys, "next_offset")
			o.vals = append(o.vals, intNode(end))
		}
		return o.String()
	}
	alignDown := func(e int) int {
		for i := 0; i < utf8.UTFMax-1 && e > start && e < len(content) && !utf8.RuneStart(content[e]); i++ {
			e--
		}
		return e
	}
	end := alignDown(min(start+MaxReadChars, len(content)))
	for {
		s := build(end)
		if len(s) <= replyCap || end <= start {
			break
		}
		// Escaping can expand a byte up to 6x; shrink by the overshoot.
		end = alignDown(max(end-max((len(s)-replyCap)/6, 1), start))
	}
	if end <= start && start < len(content) { // guarantee progress
		_, size := utf8.DecodeRuneInString(content[start:])
		end = start + size
	}
	return json.RawMessage(build(end))
}

func intNode(i int) *node { return &node{kind: kindScalar, raw: []byte(fmt.Sprint(i))} }
