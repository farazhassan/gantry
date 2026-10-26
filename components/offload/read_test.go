package offload

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/farazhassan/gantry"
)

func seeded(t *testing.T, content string) (Store, string) {
	t.Helper()
	store := NewMemStore()
	c := newComp(store)
	s := &gantry.State{Messages: history(content, 1)}
	c.shrink(withScope("chat1"), s)
	meta, ok := previewMeta(s.Messages[2].Content)
	if !ok {
		t.Fatal("seed: not shrunk")
	}
	return store, meta.Ref
}

func read(t *testing.T, store Store, args string) (map[string]json.RawMessage, error) {
	t.Helper()
	out, err := ReadTool(store, scopeFromCtx).Invoke(withScope("chat1"), json.RawMessage(args))
	if err != nil {
		return nil, err
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatalf("reply not JSON: %v\n%s", err, out)
	}
	if len(out) > MaxReadChars+200 {
		t.Errorf("reply is %d chars", len(out))
	}
	return m, nil
}

func TestReadByRefPathAndPage(t *testing.T) {
	store, ref := seeded(t, bigMetric(400))
	m, err := read(t, store, `{"ref":"`+ref+`","path":"series[0].points","offset":10,"limit":5}`)
	if err != nil {
		t.Fatal(err)
	}
	var pts []map[string]any
	_ = json.Unmarshal(m["value"], &pts)
	if len(pts) != 5 || pts[0]["value"] != float64(10) {
		t.Errorf("page = %s", m["value"])
	}
	if string(m["total"]) != "400" || string(m["next_offset"]) != "15" {
		t.Errorf("total/next = %s/%s", m["total"], m["next_offset"])
	}
}

func TestReadBySrcHandle(t *testing.T) {
	content := `{"output":"{\"items\":[{\"src\":\"mtr_q\",\"result\":{\"value\":7,\"pad\":\"` + strings.Repeat("x", 5000) + `\"}}]}"}`
	store, _ := seeded(t, content)
	m, err := read(t, store, `{"ref":"mtr_q","path":"result.value"}`)
	if err != nil {
		t.Fatal(err)
	}
	if string(m["value"]) != "7" || string(m["path"]) != `"result.value"` {
		t.Errorf("reply = %v", m)
	}
}

func TestReadCapsLargeValues(t *testing.T) {
	store, ref := seeded(t, bigMetric(400))
	m, err := read(t, store, `{"ref":"`+ref+`"}`)
	if err != nil {
		t.Fatal(err)
	}
	v := string(m["value"])
	for _, want := range []string{`"src"`, `"value":4200`, `"vs_prior_period"`, "read_result path="} {
		if !strings.Contains(v, want) {
			t.Errorf("root read lacks %s: %.400s", want, v)
		}
	}
	if m["next_offset"] != nil {
		t.Errorf("unpaged read should not page: %s", m["next_offset"])
	}
	// A paged read of the oversized field also comes back previewed.
	m, err = read(t, store, `{"ref":"`+ref+`","path":"series[0]","offset":1}`)
	if err != nil || !strings.Contains(string(m["value"]), "read_result path=") {
		t.Errorf("over-size field not previewed: %.200s %v", m["value"], err)
	}
}

func TestReadErrors(t *testing.T) {
	store, ref := seeded(t, bigMetric(400))
	for _, args := range []string{`{}`, `{"ref":"res_0000000000000000"}`, `{"ref":"mtr_missing"}`, `{"ref":"` + ref + `","path":"nope"}`} {
		if _, err := read(t, store, args); err == nil {
			t.Errorf("read(%s): want error", args)
		}
	}
	if _, err := ReadTool(store, scopeFromCtx).Invoke(context.Background(), json.RawMessage(`{"ref":"`+ref+`"}`)); err == nil {
		t.Error("read without a scope: want error")
	}
}

func TestReadToolDefinition(t *testing.T) {
	d := ReadTool(NewMemStore(), scopeFromCtx).Definition()
	if d.Name != ReadToolName || !json.Valid(d.Schema) || !strings.Contains(string(d.Schema), `"required":["ref"]`) {
		t.Errorf("definition = %+v", d)
	}
}

func TestReadObjectPaging(t *testing.T) {
	store, ref := seeded(t, "{"+kFields(2000)+"}")
	m, err := read(t, store, `{"ref":"`+ref+`","offset":50,"limit":20}`)
	if err != nil {
		t.Fatal(err)
	}
	root, err := parseJSON(m["value"])
	if err != nil || root.kind != kindObject || len(root.keys) != 20 {
		t.Fatalf("value = %s (%v)", m["value"], err)
	}
	for i, k := range root.keys {
		if k != fmt.Sprintf("k%d", 50+i) {
			t.Fatalf("key %d = %s", i, k)
		}
	}
	if string(m["total"]) != "2000" || string(m["next_offset"]) != "70" {
		t.Errorf("total/next = %s/%s", m["total"], m["next_offset"])
	}
}

func TestReadObjectPagingOffsetsAndSmallObjects(t *testing.T) {
	store, ref := seeded(t, `{"a":1,"pad":"`+strings.Repeat("x", 5000)+`"}`)
	m, err := read(t, store, `{"ref":"`+ref+`","offset":5}`)
	if err != nil {
		t.Fatal(err)
	}
	if string(m["value"]) != "{}" || string(m["total"]) != "2" || m["next_offset"] != nil {
		t.Errorf("past-the-end object page = %v", m)
	}
	m, err = read(t, store, `{"ref":"`+ref+`","offset":-3,"limit":-1}`)
	if err != nil || !strings.Contains(string(m["value"]), `"pad"`) {
		t.Errorf("negative offset/limit: %v %v", m, err)
	}
	// Past the end of an array: empty page, no panic.
	store2, ref2 := seeded(t, bigMetric(400))
	m, err = read(t, store2, `{"ref":"`+ref2+`","path":"series[0].points","offset":400}`)
	if err != nil || string(m["value"]) != "[]" || string(m["total"]) != "400" {
		t.Errorf("array past the end: %v %v", m, err)
	}
}

var hintRe = regexp.MustCompile(`read_result path=("(?:[^"\\]|\\.)*") offset=(\d+)`)

// hintsIn returns the (path, offset) of every paging hint in JSON text s.
func hintsIn(s string) (out [][2]string) {
	var decoded []string
	var walk func(v any)
	walk = func(v any) {
		switch x := v.(type) {
		case string:
			decoded = append(decoded, x)
		case []any:
			for _, e := range x {
				walk(e)
			}
		case map[string]any:
			for _, e := range x {
				walk(e)
			}
		}
	}
	var v any
	if json.Unmarshal([]byte(s), &v) == nil {
		walk(v)
	}
	for _, d := range decoded {
		for _, m := range hintRe.FindAllStringSubmatch(d, -1) {
			if p, err := strconv.Unquote(m[1]); err == nil {
				out = append(out, [2]string{p, m[2]})
			}
		}
	}
	return out
}

func TestReadHintsAreFollowable(t *testing.T) {
	for name, content := range map[string]string{
		"metric": bigMetric(400),
		"object": "{" + kFields(2000) + "}",
	} {
		store, ref := seeded(t, content)
		root, _ := parseJSON([]byte(content))
		texts := []string{buildPreview(root, content, ref, 2000, keepSet(DefaultKeepKeys))}
		m, err := read(t, store, `{"ref":"`+ref+`"}`)
		if err != nil {
			t.Fatal(err)
		}
		texts = append(texts, string(m["value"]))
		if name == "metric" {
			m, err = read(t, store, `{"ref":"`+ref+`","path":"series[0]","offset":1}`)
			if err != nil {
				t.Fatal(err)
			}
			texts = append(texts, string(m["value"]))
		}
		followed := 0
		for _, txt := range texts {
			for _, h := range hintsIn(txt) {
				args, _ := json.Marshal(map[string]any{"ref": ref, "path": h[0], "offset": atoi(h[1]), "limit": 3})
				r, err := read(t, store, string(args))
				if err != nil {
					t.Fatalf("%s: hint %v: %v", name, h, err)
				}
				if v := string(r["value"]); v == "" || v == "[]" || v == "{}" {
					t.Errorf("%s: hint %v gave an empty page", name, h)
				}
				followed++
			}
		}
		if followed < 1+len(texts)/3 {
			t.Errorf("%s: followed only %d hints", name, followed)
		}
	}
}

func TestReadObjectMoreHintLeadsToItsField(t *testing.T) {
	content := "{" + kFields(2000) + "}"
	store, ref := seeded(t, content)
	root, _ := parseJSON([]byte(content))
	hs := hintsIn(buildPreview(root, content, ref, 3000, keepSet(DefaultKeepKeys)))
	if len(hs) != 1 || hs[0][0] != "" {
		t.Fatalf("hints = %v", hs)
	}
	m, err := read(t, store, `{"ref":"`+ref+`","offset":`+hs[0][1]+`,"limit":1}`)
	if err != nil {
		t.Fatal(err)
	}
	if want := fmt.Sprintf(`{"k%s":%s}`, hs[0][1], hs[0][1]); string(m["value"]) != want {
		t.Errorf("value = %s, want %s", m["value"], want)
	}
}

func atoi(s string) int { n, _ := strconv.Atoi(s); return n }

func TestReadOversizedElement(t *testing.T) {
	store, ref := seeded(t, `{"rows":[{"blob":"`+strings.Repeat("y", 20000)+`","a":[`+strings.Repeat("1,", 5000)+`1]}]}`)
	m, err := read(t, store, `{"ref":"`+ref+`","path":"rows"}`)
	if err != nil {
		t.Fatal(err)
	}
	if string(m["total"]) != "1" || m["next_offset"] != nil {
		t.Errorf("total/next = %s/%s", m["total"], m["next_offset"])
	}
	var arr []any
	if json.Unmarshal(m["value"], &arr) != nil || len(arr) != 1 {
		t.Errorf("value = %.200s", m["value"])
	}
}

func TestReadOversizedObjectField(t *testing.T) {
	store, ref := seeded(t, `{"a":{"blob":"`+strings.Repeat("y", 20000)+`","l":[`+strings.Repeat("1,", 5000)+`1]},"b":1}`)
	m, err := read(t, store, `{"ref":"`+ref+`","limit":1}`)
	if err != nil {
		t.Fatal(err)
	}
	if string(m["total"]) != "2" || string(m["next_offset"]) != "1" {
		t.Errorf("total/next = %s/%s", m["total"], m["next_offset"])
	}
}

func TestReadNonJSONPagesByByteOffset(t *testing.T) {
	text := strings.Repeat("héllo 世界 \"q\"\n", 3000)
	store, ref := seeded(t, text)
	var got strings.Builder
	offset, pages := 0, 0
	for {
		m, err := read(t, store, fmt.Sprintf(`{"ref":%q,"offset":%d}`, ref, offset))
		if err != nil {
			t.Fatal(err)
		}
		var v string
		if err := json.Unmarshal(m["value"], &v); err != nil || !utf8.ValidString(v) {
			t.Fatalf("page invalid: %v", err)
		}
		if string(m["total"]) != strconv.Itoa(len(text)) {
			t.Fatalf("total = %s", m["total"])
		}
		got.WriteString(v)
		pages++
		if m["next_offset"] == nil {
			break
		}
		next := atoi(string(m["next_offset"]))
		if next <= offset {
			t.Fatalf("no progress: %d -> %d", offset, next)
		}
		offset = next
	}
	if got.String() != text || pages < 2 {
		t.Errorf("reassembled %d bytes of %d in %d pages", got.Len(), len(text), pages)
	}
	m, err := read(t, store, `{"ref":"`+ref+`","offset":2}`)
	if err != nil {
		t.Fatal(err)
	}
	var v string
	if json.Unmarshal(m["value"], &v) != nil || !utf8.ValidString(v) || !strings.HasPrefix(v, "éllo") {
		t.Errorf("mid-rune start: %.40q", v)
	}
	if _, err := read(t, store, `{"ref":"`+ref+`","path":"x"}`); err == nil {
		t.Error("path on non-JSON: want error")
	}
}

func nItems(n int) string {
	var s []string
	for i := 0; i < n; i++ {
		s = append(s, fmt.Sprintf(`{"n":%d}`, i))
	}
	return "[" + strings.Join(s, ",") + "]"
}

func TestReadSrcHintsAreRelativeToTheHandle(t *testing.T) {
	content := `{"items":[{"src":"mtr_0","series":` + nItems(2000) + `},{"src":"mtr_1","series":` + nItems(2000) + `}]}`
	store, _ := seeded(t, content)
	m, err := read(t, store, `{"ref":"mtr_1"}`)
	if err != nil {
		t.Fatal(err)
	}
	if string(m["ref"]) != `"mtr_1"` {
		t.Errorf("ref echoed as %s", m["ref"])
	}
	hs := hintsIn(string(m["value"]))
	if len(hs) == 0 || hs[0][0] != "series" {
		t.Fatalf("hints = %v in %.300s", hs, m["value"])
	}
	args, _ := json.Marshal(map[string]any{"ref": "mtr_1", "path": hs[0][0], "offset": atoi(hs[0][1]), "limit": 2})
	m, err = read(t, store, string(args))
	if err != nil {
		t.Fatal(err)
	}
	if string(m["value"]) != `[{"n":3},{"n":4}]` || string(m["path"]) != `"series"` {
		t.Errorf("reply = %s / %s", m["value"], m["path"])
	}
}

func TestReadPagesLongStrings(t *testing.T) {
	body := strings.Repeat("wörd ", 4000)
	store, ref := seeded(t, `{"body":`+string(marshalString(body))+`}`)
	var got strings.Builder
	offset := 0
	for i := 0; i < 20; i++ {
		m, err := read(t, store, fmt.Sprintf(`{"ref":%q,"path":"body","offset":%d}`, ref, offset))
		if err != nil {
			t.Fatal(err)
		}
		var v string
		if json.Unmarshal(m["value"], &v) != nil || !utf8.ValidString(v) || string(m["total"]) != strconv.Itoa(len(body)) {
			t.Fatalf("bad page: %.100s total=%s", m["value"], m["total"])
		}
		got.WriteString(v)
		if m["next_offset"] == nil {
			break
		}
		next := atoi(string(m["next_offset"]))
		if next <= offset {
			t.Fatal("no progress")
		}
		offset = next
	}
	if got.String() != body {
		t.Errorf("reassembled %d of %d bytes", got.Len(), len(body))
	}
	// A JSON root that is a string pages too.
	store2, ref2 := seeded(t, string(marshalString(body)))
	m, err := read(t, store2, `{"ref":"`+ref2+`"}`)
	if err != nil || m["next_offset"] == nil {
		t.Errorf("string root: %v %v", m["next_offset"], err)
	}
}

func TestReadHugeKeyInPagedObject(t *testing.T) {
	store, ref := seeded(t, `{"`+strings.Repeat("k", 20000)+`":1,"b":2}`)
	m, err := read(t, store, `{"ref":"`+ref+`","offset":0,"limit":1}`)
	if err != nil {
		t.Fatal(err)
	}
	if string(m["total"]) != "2" || string(m["next_offset"]) != "1" {
		t.Errorf("total/next = %s/%s", m["total"], m["next_offset"])
	}
}

func TestReadErrorsAreCappedAndHelpful(t *testing.T) {
	store, ref := seeded(t, `{"alpha":1,"list":[1,2,3],"pad":"`+strings.Repeat("x", 5000)+`"}`)
	long := strings.Repeat("z", 5000)
	for _, args := range []string{`{"ref":"` + long + `"}`, `{"ref":"` + ref + `","path":"` + long + `"}`} {
		_, err := read(t, store, args)
		if err == nil || len(err.Error()) > 700 {
			t.Errorf("error = %v", err)
		}
	}
	_, err := read(t, store, `{"ref":"`+ref+`","path":"nope"}`)
	if err == nil || !strings.Contains(err.Error(), "alpha") || !strings.Contains(err.Error(), "list") {
		t.Errorf("missing field error lacks field names: %v", err)
	}
	_, err = read(t, store, `{"ref":"`+ref+`","path":"list[9]"}`)
	if err == nil || !strings.Contains(err.Error(), "3") {
		t.Errorf("missing element error lacks length: %v", err)
	}
	_, err = read(t, store, `{"ref":"`+ref+`","path":"x[\\\"a.b\\\"]"}`)
	if err == nil || !strings.Contains(err.Error(), `["x.y"]`) {
		t.Errorf("no quoting hint: %v", err)
	}
}

func TestReadResRefFallsBackToSrcIndex(t *testing.T) {
	store, _ := seeded(t, `{"items":[{"src":"res_fake","v":1,"pad":"`+strings.Repeat("x", 5000)+`"}]}`)
	m, err := read(t, store, `{"ref":"res_fake","path":"v"}`)
	if err != nil || string(m["value"]) != "1" {
		t.Errorf("fallback: %v %v", m, err)
	}
}

func TestReadPathEchoLimit(t *testing.T) {
	store, ref := seeded(t, bigMetric(400))
	long := strings.Repeat("a", 1500)
	_, err := read(t, store, `{"ref":"`+ref+`","path":"`+long+`"}`)
	if err == nil {
		t.Error("want error")
	}
	m, err := read(t, store, `{"ref":"`+ref+`","path":"series[0].points","limit":1}`)
	if err != nil || m["path"] == nil {
		t.Errorf("short path should echo: %v", err)
	}
}

func TestReadSummaryOfThePathJustReadPages(t *testing.T) {
	store, ref := seeded(t, `{"`+"a"+strings.Repeat("k", 5000)+`":1,"`+"b"+strings.Repeat("k", 5000)+`":2,"`+"c"+strings.Repeat("k", 5000)+`":3}`)
	m, err := read(t, store, `{"ref":"`+ref+`"}`)
	if err != nil {
		t.Fatal(err)
	}
	hs := hintsIn(string(m["value"]))
	if len(hs) != 1 || hs[0][0] != "" {
		t.Fatalf("value = %.300s hints = %v", m["value"], hs)
	}
	if !strings.Contains(string(m["value"]), "limit=20") {
		t.Errorf("hint lacks limit: %s", m["value"])
	}
	m, err = read(t, store, `{"ref":"`+ref+`","offset":`+hs[0][1]+`,"limit":20}`)
	if err != nil || string(m["total"]) != "3" {
		t.Errorf("following the hint: %v %v", m, err)
	}
}
