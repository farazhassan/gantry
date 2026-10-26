package offload

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"
)

// DefaultKeepKeys are kept whole in every preview: provenance, coverage
// and headline numbers a model needs to keep reasoning without a fetch.
// Treat it as read-only.
var DefaultKeepKeys = []string{"src", "coverage", "capped", "notes", "value", "vs_prior_period"}

// metaKey is the preview's trailer field. Content carrying it is a preview.
const metaKey = "_offloaded"

// maxStringRunes is how much of a long string value a preview keeps.
const maxStringRunes = 200

type offloadedMeta struct {
	Ref   string `json:"ref"`
	Chars int    `json:"chars"`
}

// level is one step of the fitting ladder: keep up to k elements of each
// array, up to keys non-keep fields of each object, and descend at most
// depth levels before summarizing.
type level struct{ k, depth, keys int }

var levels = []level{{3, 64, 50}, {2, 64, 30}, {1, 64, 20}, {0, 64, 20}, {1, 4, 12}, {0, 3, 8}, {0, 2, 6}, {0, 1, 4}}

func keepSet(keys []string) map[string]bool {
	m := make(map[string]bool, len(keys))
	for _, k := range keys {
		m[k] = true
	}
	return m
}

type previewer struct {
	budget int
	keep   map[string]bool
}

// buildPreview renders content's preview: a JSON object holding a budgeted
// summary of root (nil for non-JSON content) and a trailer naming the ref.
// It depends only on its arguments, so equal content gives equal previews.
// The result fits budget unless the trailer alone is larger. ref must start
// with "res_", or previewMeta will not recognise the preview. Budgets and
// the trailer's chars count bytes, not runes.
func buildPreview(root *node, content, ref string, budget int, keep map[string]bool) string {
	meta := &node{kind: kindObject,
		keys: []string{"ref", "chars", "read"},
		vals: []*node{
			textNode(ref),
			{kind: kindScalar, raw: []byte(fmt.Sprint(len(content)))},
			textNode(fmt.Sprintf("read_result(ref=%q, path, offset, limit) for the full data", ref)),
		}}
	if root == nil {
		room := max(budget-len(meta.String())-40, 100)
		for {
			s := wrap(&node{kind: kindObject, keys: []string{"head"}, vals: []*node{textNode(cutBytes(content, room))}}, meta).String()
			if len(s) <= budget || room <= 0 {
				return s
			}
			room = max(room-(len(s)-budget), 0)
		}
	}
	p := previewer{budget: budget, keep: keep}
	for _, l := range levels {
		if s := wrap(p.render(root, "", 0, l), meta).String(); len(s) <= budget {
			return s
		}
	}
	reserve := len(wrap(&node{kind: kindObject}, meta).String())
	return wrap(p.keepOnly(root, reserve), meta).String()
}

// wrap adds the trailer to an object preview, or nests a non-object preview
// under "value".
func wrap(pv, meta *node) *node {
	if pv.kind != kindObject {
		pv = &node{kind: kindObject, keys: []string{"value"}, vals: []*node{pv}}
	}
	out := &node{kind: kindObject}
	for i, k := range pv.keys {
		if k != metaKey { // the trailer key must stay unique
			out.keys = append(out.keys, k)
			out.vals = append(out.vals, pv.vals[i])
		}
	}
	out.keys = append(out.keys, metaKey)
	out.vals = append(out.vals, meta)
	return out
}

// fit renders n within the budget without a trailer (for read_result
// replies), summarizing it if nothing fits.
func (p previewer) fit(n *node, path string) *node {
	for _, l := range levels {
		if pv := p.render(n, path, 0, l); len(pv.String()) <= p.budget {
			return pv
		}
	}
	if s := summary(n, path); len(s.String()) <= p.budget {
		return s
	}
	return textNode("value too large to show; read a narrower path")
}

func (p previewer) render(n *node, path string, depth int, l level) *node {
	switch n.kind {
	case kindObject:
		out := &node{kind: kindObject}
		shown, dropped, next := 0, 0, 0
		for i, k := range n.keys {
			v, vp := n.vals[i], keyPath(path, k)
			var pv *node
			kept := p.keep[k] && len(v.String()) <= p.budget/2
			if !kept {
				if shown >= l.keys {
					dropped++
					continue
				}
				shown++
				next = i + 1
			}
			switch {
			case kept:
				pv = v
			case v.kind == kindScalar:
				pv = cutScalar(v)
			case depth+1 >= l.depth:
				pv = summary(v, vp)
			default:
				pv = p.render(v, vp, depth+1, l)
			}
			out.keys = append(out.keys, k)
			out.vals = append(out.vals, pv)
		}
		if dropped > 0 {
			// offset indexes ALL of the object's fields in document order
			// (keep keys included): it is the index just after the last
			// non-keep field shown. read_result pages fields by offset/limit
			// over that same order, so keep keys after the offset may be
			// shown again. The object's own "_more" field is left out of
			// the preview so the key stays unique.
			kept := &node{kind: kindObject}
			for i, k := range out.keys {
				if k != "_more" {
					kept.keys = append(kept.keys, k)
					kept.vals = append(kept.vals, out.vals[i])
				}
			}
			out = kept
			out.keys = append(out.keys, "_more")
			out.vals = append(out.vals, textNode(fmt.Sprintf("%d more fields: read_result path=%q offset=%d", dropped, path, next)))
		}
		return out
	case kindArray:
		out := &node{kind: kindArray}
		k := min(l.k, len(n.elems))
		for i := 0; i < k; i++ {
			e, ep := n.elems[i], indexPath(path, i)
			switch {
			case e.kind == kindScalar:
				out.elems = append(out.elems, cutScalar(e))
			case depth+1 >= l.depth:
				out.elems = append(out.elems, summary(e, ep))
			default:
				out.elems = append(out.elems, p.render(e, ep, depth+1, l))
			}
		}
		if rest := len(n.elems) - k; rest > 0 {
			out.elems = append(out.elems, textNode(fmt.Sprintf("… %d more: read_result path=%q offset=%d", rest, path, k)))
		}
		return out
	}
	return cutScalar(n)
}

// keepOnly is the last resort: only the keep keys of an object root. It
// drops keep keys from the end until the result plus reserve (the size of
// the trailer wrapper) fits the budget.
func (p previewer) keepOnly(root *node, reserve int) *node {
	if root.kind != kindObject {
		return summary(root, "")
	}
	out := &node{kind: kindObject}
	for i, k := range root.keys {
		if p.keep[k] && len(root.vals[i].String()) <= p.budget/2 {
			out.keys = append(out.keys, k)
			out.vals = append(out.vals, root.vals[i])
		}
	}
	for len(out.keys) > 0 && len(out.String())+reserve > p.budget {
		out.keys = out.keys[:len(out.keys)-1]
		out.vals = out.vals[:len(out.vals)-1]
	}
	return out
}

func summary(n *node, path string) *node {
	switch n.kind {
	case kindObject:
		return textNode(fmt.Sprintf("{%d fields: read_result path=%q}", len(n.keys), path))
	case kindArray:
		return textNode(fmt.Sprintf("[%d items: read_result path=%q]", len(n.elems), path))
	}
	return cutScalar(n)
}

func cutScalar(n *node) *node {
	if len(n.raw) > maxStringRunes && n.raw[0] != '"' {
		return textNode(cutRunes(string(n.raw), maxStringRunes))
	}
	if s, ok := n.str(); ok && utf8.RuneCountInString(s) > maxStringRunes {
		return textNode(cutRunes(s, maxStringRunes))
	}
	return n
}

func cutRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	i := 0
	for j := range s {
		if i == n {
			return s[:j] + "…"
		}
		i++
	}
	return s
}

// cutBytes keeps a prefix of s of at most n bytes (ellipsis included),
// cut on a rune boundary.
func cutBytes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	end := max(n-len("…"), 0)
	for end > 0 && !utf8.RuneStart(s[end]) {
		end--
	}
	return s[:end] + "…"
}

// previewMeta reports whether content is a preview this package wrote, and
// its trailer: the last top-level key is _offloaded, holding a res_ ref, a
// positive chars count and a read hint.
func previewMeta(content string) (offloadedMeta, bool) {
	if !strings.Contains(content, `"`+metaKey+`":{"ref":"res_`) {
		return offloadedMeta{}, false
	}
	if len(content) > 64<<10 || !strings.HasSuffix(strings.TrimRight(content, " \t\r\n"), "}}") {
		return offloadedMeta{}, false
	}
	root, err := parseJSON([]byte(content))
	if err != nil || root.kind != kindObject || len(root.keys) == 0 || root.keys[len(root.keys)-1] != metaKey {
		return offloadedMeta{}, false
	}
	o := root.vals[len(root.vals)-1]
	ref, _ := o.get("ref").str()
	read, _ := o.get("read").str()
	var chars int
	if c := o.get("chars"); c == nil || c.kind != kindScalar || json.Unmarshal(c.raw, &chars) != nil {
		return offloadedMeta{}, false
	}
	if !strings.HasPrefix(ref, "res_") || chars <= 0 || read == "" {
		return offloadedMeta{}, false
	}
	return offloadedMeta{Ref: ref, Chars: chars}, true
}
