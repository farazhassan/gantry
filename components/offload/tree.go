package offload

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

type kind uint8

const (
	kindScalar kind = iota
	kindObject
	kindArray
)

// node is a decoded JSON value that keeps object key order, so previews and
// read_result replies list fields the way the tool wrote them.
type node struct {
	kind  kind
	keys  []string
	vals  []*node // object values, parallel to keys
	elems []*node // array elements
	raw   []byte  // a scalar, as JSON
}

// parseJSON decodes data. A string value that itself holds a JSON object or
// array is decoded as that nested value (query_data's output is one), so
// previews, paths and src handles see through it.
func parseJSON(data []byte) (*node, error) { return parseJSONAt(data, 0) }

// maxDepth caps how deeply objects and arrays may nest, so a hostile result
// cannot overflow the stack of the recursive walkers.
const maxDepth = 10000

func parseJSONAt(data []byte, depth int) (*node, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	n, err := parseValue(dec, depth)
	if err != nil {
		return nil, err
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, errors.New("offload: trailing data after JSON value")
	}
	return n, nil
}

func parseValue(dec *json.Decoder, depth int) (*node, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	switch t := tok.(type) {
	case json.Delim:
		if (t == '{' || t == '[') && depth+1 > maxDepth {
			return nil, errors.New("offload: JSON nested too deeply")
		}
		switch t {
		case '{':
			n := &node{kind: kindObject}
			for dec.More() {
				kt, err := dec.Token()
				if err != nil {
					return nil, err
				}
				k, ok := kt.(string)
				if !ok {
					return nil, fmt.Errorf("offload: object key %v is not a string", kt)
				}
				v, err := parseValue(dec, depth+1)
				if err != nil {
					return nil, err
				}
				n.keys = append(n.keys, k)
				n.vals = append(n.vals, v)
			}
			if _, err := dec.Token(); err != nil {
				return nil, err
			}
			return n, nil
		case '[':
			n := &node{kind: kindArray}
			for dec.More() {
				v, err := parseValue(dec, depth+1)
				if err != nil {
					return nil, err
				}
				n.elems = append(n.elems, v)
			}
			if _, err := dec.Token(); err != nil {
				return nil, err
			}
			return n, nil
		}
		return nil, fmt.Errorf("offload: unexpected delimiter %v", t)
	case string:
		if s := strings.TrimSpace(t); len(s) > 1 && (s[0] == '{' || s[0] == '[') {
			if nested, err := parseJSONAt([]byte(s), depth); err == nil {
				return nested, nil
			}
		}
		return &node{kind: kindScalar, raw: marshalString(t)}, nil
	default:
		raw, err := json.Marshal(t)
		if err != nil {
			return nil, err
		}
		return &node{kind: kindScalar, raw: raw}, nil
	}
}

// marshalString encodes s without escaping <, > and &, which the model
// reads more easily.
func marshalString(s string) []byte {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(s)
	return bytes.TrimRight(b.Bytes(), "\n")
}

func textNode(s string) *node { return &node{kind: kindScalar, raw: marshalString(s)} }

func (n *node) encode(b *bytes.Buffer) {
	switch n.kind {
	case kindObject:
		b.WriteByte('{')
		for i, k := range n.keys {
			if i > 0 {
				b.WriteByte(',')
			}
			b.Write(marshalString(k))
			b.WriteByte(':')
			n.vals[i].encode(b)
		}
		b.WriteByte('}')
	case kindArray:
		b.WriteByte('[')
		for i, e := range n.elems {
			if i > 0 {
				b.WriteByte(',')
			}
			e.encode(b)
		}
		b.WriteByte(']')
	default:
		b.Write(n.raw)
	}
}

// String encodes n as compact JSON. Nested JSON that was decoded from a
// string is encoded as plain JSON, not re-escaped into a string.
func (n *node) String() string {
	var b bytes.Buffer
	n.encode(&b)
	return b.String()
}

func (n *node) get(key string) *node {
	if n.kind != kindObject {
		return nil
	}
	for i, k := range n.keys {
		if k == key {
			return n.vals[i]
		}
	}
	return nil
}

// str returns n's value if n is a JSON string.
func (n *node) str() (string, bool) {
	if n == nil || n.kind != kindScalar || len(n.raw) == 0 || n.raw[0] != '"' {
		return "", false
	}
	var s string
	if json.Unmarshal(n.raw, &s) != nil {
		return "", false
	}
	return s, true
}

type step struct {
	key     string
	index   int
	isIndex bool
}

// parsePath reads dot-and-bracket paths such as items[0].result.series.
func parsePath(p string) ([]step, error) {
	var steps []step
	q0 := cutBytes(p, 200) // errors echo at most this much of the input
	for i := 0; i < len(p); {
		switch p[i] {
		case '.':
			i++
		case '[':
			if strings.HasPrefix(p[i:], `[\"`) {
				return nil, fmt.Errorf("bad path %q: write quoted keys as [\"x.y\"] without backslashes", q0)
			}
			if i+1 < len(p) && p[i+1] == '"' {
				q, err := strconv.QuotedPrefix(p[i+1:])
				if err != nil {
					return nil, fmt.Errorf("bad quoted key in path %q", q0)
				}
				key, err := strconv.Unquote(q)
				if err != nil {
					return nil, fmt.Errorf("bad quoted key in path %q", q0)
				}
				end := i + 1 + len(q)
				if end >= len(p) || p[end] != ']' {
					return nil, fmt.Errorf("unclosed [ in path %q", q0)
				}
				steps = append(steps, step{key: key})
				i = end + 1
				break
			}
			j := strings.IndexByte(p[i:], ']')
			if j < 0 {
				return nil, fmt.Errorf("unclosed [ in path %q", q0)
			}
			idx, err := strconv.Atoi(p[i+1 : i+j])
			if err != nil || idx < 0 {
				return nil, fmt.Errorf("bad index %q in path %q", cutBytes(p[i+1:i+j], 50), q0)
			}
			steps = append(steps, step{index: idx, isIndex: true})
			i += j + 1
		default:
			j := i
			for j < len(p) && p[j] != '.' && p[j] != '[' {
				j++
			}
			steps = append(steps, step{key: p[i:j]})
			i = j
		}
	}
	return steps, nil
}

// at returns the value at path ("" is n itself).
func (n *node) at(path string) (*node, error) {
	steps, err := parsePath(path)
	if err != nil {
		return nil, err
	}
	cur := n
	for _, s := range steps {
		if s.isIndex {
			if cur.kind != kindArray {
				return nil, fmt.Errorf("path %q: [%d] applied to %s, not an array", cutBytes(path, 200), s.index, describe(cur))
			}
			if s.index >= len(cur.elems) {
				return nil, fmt.Errorf("path %q: no element [%d]; the array has %d elements", cutBytes(path, 200), s.index, len(cur.elems))
			}
			cur = cur.elems[s.index]
			continue
		}
		v := cur.get(s.key)
		if v == nil {
			return nil, fmt.Errorf("path %q: no field %q; here: %s", cutBytes(path, 200), cutBytes(s.key, 50), describe(cur))
		}
		cur = v
	}
	return cur, nil
}

// describe says what a node holds, for path errors: an object's field names
// (up to 20), an array's length, or that it is a scalar.
func describe(n *node) string {
	switch n.kind {
	case kindObject:
		var names []string
		for i, k := range n.keys {
			if i == 20 {
				names = append(names, fmt.Sprintf("… %d more", len(n.keys)-20))
				break
			}
			names = append(names, strconv.Quote(cutBytes(k, 50)))
		}
		return "an object with fields " + strings.Join(names, ", ")
	case kindArray:
		return fmt.Sprintf("an array of %d elements", len(n.elems))
	}
	return "a value, not an object"
}

// keyPath appends an object key to a path. A key that is empty or holds a
// path delimiter or quote is written as a quoted bracket segment.
func keyPath(base, key string) string {
	if key == "" || strings.ContainsAny(key, `.[]"`) {
		return base + "[" + strconv.Quote(key) + "]"
	}
	if base == "" {
		return key
	}
	return base + "." + key
}

func indexPath(base string, i int) string { return base + "[" + strconv.Itoa(i) + "]" }

// joinPaths appends a relative path to a base path.
func joinPaths(base, rel string) string {
	switch {
	case base == "":
		return rel
	case rel == "":
		return base
	case rel[0] == '[':
		return base + rel
	}
	return base + "." + rel
}

type srcPath struct{ src, path string }

// srcPaths returns the path of every object with a string "src" field, in
// document order, keeping the first path of a handle that appears twice.
func srcPaths(root *node) []srcPath {
	var out []srcPath
	seen := map[string]bool{}
	var walk func(n *node, path string)
	walk = func(n *node, path string) {
		switch n.kind {
		case kindObject:
			if s, ok := n.get("src").str(); ok && s != "" && !seen[s] {
				seen[s] = true
				out = append(out, srcPath{s, path})
			}
			for i, k := range n.keys {
				walk(n.vals[i], keyPath(path, k))
			}
		case kindArray:
			for i, e := range n.elems {
				walk(e, indexPath(path, i))
			}
		}
	}
	walk(root, "")
	return out
}
