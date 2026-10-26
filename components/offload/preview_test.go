package offload

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func bigMetric(points int) string {
	var pts []string
	for i := 0; i < points; i++ {
		pts = append(pts, fmt.Sprintf(`{"t":"2026-10-%02d","value":%d,"vs_prior_period":0.1}`, i%28+1, i))
	}
	return `{"src":"mtr_abc","coverage":{"completeness":1},"measure":"orders","value":4200,` +
		`"vs_prior_period":-0.08,"series":[{"dims":{"channel":"web"},"points":[` + strings.Join(pts, ",") + `]}]}`
}

func TestPreviewFitsBudgetKeepsKeysAndIsValidJSON(t *testing.T) {
	content := bigMetric(400)
	root, err := parseJSON([]byte(content))
	if err != nil {
		t.Fatal(err)
	}
	p := buildPreview(root, content, "res_0123456789abcdef", 1500, keepSet(DefaultKeepKeys))
	if len(p) > 1500 {
		t.Errorf("preview is %d chars, want <= 1500", len(p))
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(p), &got); err != nil {
		t.Fatalf("preview is not JSON: %v\n%s", err, p)
	}
	for _, k := range []string{"src", "coverage", "value", "vs_prior_period", "measure", "_offloaded"} {
		if _, ok := got[k]; !ok {
			t.Errorf("preview lacks %q: %s", k, p)
		}
	}
	meta, ok := previewMeta(p)
	if !ok || meta.Ref != "res_0123456789abcdef" || meta.Chars != len(content) {
		t.Errorf("previewMeta = %+v, %v", meta, ok)
	}
	// The hint sits inside a JSON string, so its quotes are escaped.
	if !strings.Contains(p, `more: read_result path=\"series[0].points\" offset=`) {
		t.Errorf("preview doesn't say how to read the rest of the points: %s", p)
	}
}

func TestPreviewIsDeterministic(t *testing.T) {
	content := bigMetric(300)
	root, _ := parseJSON([]byte(content))
	a := buildPreview(root, content, "res_x", 1500, keepSet(DefaultKeepKeys))
	root2, _ := parseJSON([]byte(content))
	b := buildPreview(root2, content, "res_x", 1500, keepSet(DefaultKeepKeys))
	if a != b {
		t.Errorf("previews differ:\n%s\n%s", a, b)
	}
}

func TestPreviewOfNonJSONKeepsAHead(t *testing.T) {
	content := strings.Repeat("plain text ", 1000)
	p := buildPreview(nil, content, "res_y", 1500, keepSet(DefaultKeepKeys))
	var got struct {
		Head      string         `json:"head"`
		Offloaded map[string]any `json:"_offloaded"`
	}
	if err := json.Unmarshal([]byte(p), &got); err != nil {
		t.Fatalf("not JSON: %v", err)
	}
	if !strings.HasPrefix(content, strings.TrimSuffix(got.Head, "…")) || got.Offloaded["ref"] != "res_y" {
		t.Errorf("preview = %s", p)
	}
	if len(p) > 1500 {
		t.Errorf("preview is %d chars", len(p))
	}
}

func TestPreviewOfNonJSONFitsBytesWithMultibyteAndEscapes(t *testing.T) {
	for _, c := range []string{strings.Repeat("日本語", 3000), strings.Repeat("\"\n", 5000)} {
		p := buildPreview(nil, c, "res_m", 1500, keepSet(DefaultKeepKeys))
		if len(p) > 1500 {
			t.Errorf("preview is %d bytes", len(p))
		}
		var got map[string]any
		if err := json.Unmarshal([]byte(p), &got); err != nil {
			t.Errorf("not JSON: %v", err)
		}
	}
}

func TestPreviewOfTopLevelArrayWrapsIt(t *testing.T) {
	content := "[" + strings.Repeat(`{"a":"`+strings.Repeat("z", 100)+`"},`, 99) + `{"a":1}]`
	root, _ := parseJSON([]byte(content))
	p := buildPreview(root, content, "res_z", 1500, keepSet(DefaultKeepKeys))
	var got map[string]json.RawMessage
	if err := json.Unmarshal([]byte(p), &got); err != nil {
		t.Fatalf("not JSON: %v", err)
	}
	if _, ok := got["value"]; !ok {
		t.Errorf("array preview lacks value: %s", p)
	}
}

func TestPreviewMetaIgnoresOrdinaryContent(t *testing.T) {
	for _, c := range []string{`{"a":1}`, `not json`, `{"_offloaded":"x"}`} {
		if _, ok := previewMeta(c); ok {
			t.Errorf("previewMeta(%s) = true", c)
		}
	}
}

func TestPreviewBudgetSweep(t *testing.T) {
	var wide []string
	for i := 0; i < 300; i++ {
		wide = append(wide, fmt.Sprintf(`"key%d":"%s"`, i, strings.Repeat("v", 50)))
	}
	var deep []string
	for i := 0; i < 500; i++ {
		deep = append(deep, fmt.Sprintf(`{"a":{"b":{"c":{"d":[%d,{"e":"x"}]}}}}`, i))
	}
	shapes := map[string]string{
		"metric": bigMetric(400),
		"wide":   "{" + strings.Join(wide, ",") + "}",
		"deep":   "[" + strings.Join(deep, ",") + "]",
		"string": `{"text":"` + strings.Repeat("s", 20000) + `"}`,
	}
	for name, c := range shapes {
		for _, budget := range []int{800, 1500, 3000} {
			root, err := parseJSON([]byte(c))
			if err != nil {
				t.Fatal(err)
			}
			p := buildPreview(root, c, "res_0123456789abcdef", budget, keepSet(DefaultKeepKeys))
			if len(p) > budget {
				t.Errorf("%s/%d: preview is %d chars", name, budget, len(p))
			}
			if !json.Valid([]byte(p)) {
				t.Errorf("%s/%d: not JSON: %s", name, budget, p)
			}
		}
	}
}

func TestPreviewOversizedKeepKeyDoesNotStayWhole(t *testing.T) {
	var notes []string
	for i := 0; i < 200; i++ {
		notes = append(notes, fmt.Sprintf(`"note number %d is fairly long text"`, i))
	}
	c := `{"value":1,"notes":[` + strings.Join(notes, ",") + `]}`
	root, _ := parseJSON([]byte(c))
	p := buildPreview(root, c, "res_n", 1500, keepSet(DefaultKeepKeys))
	if len(p) > 1500 || strings.Contains(p, "note number 150") {
		t.Errorf("oversized notes stayed whole (%d chars): %s", len(p), p)
	}
}

func TestPreviewSeveralKeepKeysNearHalfBudgetStillFit(t *testing.T) {
	big := `"` + strings.Repeat("q", 700) + `"`
	c := `{"src":` + big + `,"coverage":` + big + `,"capped":` + big + `,"x":1}`
	root, _ := parseJSON([]byte(c))
	p := buildPreview(root, c, "res_k", 1500, keepSet(DefaultKeepKeys))
	if len(p) > 1500 || !json.Valid([]byte(p)) {
		t.Errorf("preview %d chars valid=%v", len(p), json.Valid([]byte(p)))
	}
}

func TestPreviewCapsKeysOfWideObject(t *testing.T) {
	var kv []string
	for i := 0; i < 2000; i++ {
		kv = append(kv, fmt.Sprintf(`"k%d":%d`, i, i))
	}
	c := "{" + strings.Join(kv, ",") + "}"
	root, _ := parseJSON([]byte(c))
	p := buildPreview(root, c, "res_w", 1500, keepSet(DefaultKeepKeys))
	var got map[string]any
	if err := json.Unmarshal([]byte(p), &got); err != nil || len(p) > 1500 {
		t.Fatalf("len %d err %v", len(p), err)
	}
	if _, ok := got["k0"]; !ok {
		t.Errorf("no keys shown: %s", p)
	}
	if m, _ := got["_more"].(string); !strings.Contains(m, "more fields: read_result path=\"\" offset=") {
		t.Errorf("no _more hint: %s", p)
	}
}

func TestPreviewHugeKeyStaysInBudget(t *testing.T) {
	c := `{"` + strings.Repeat("k", 3000) + `":[1,2,3,4,5]}`
	root, _ := parseJSON([]byte(c))
	p := buildPreview(root, c, "res_h", 1500, keepSet(DefaultKeepKeys))
	if len(p) > 1500 || !json.Valid([]byte(p)) {
		t.Errorf("len %d valid %v", len(p), json.Valid([]byte(p)))
	}
}

func TestFitNeverExceedsBudget(t *testing.T) {
	cases := []struct {
		name, content, path string
		budget              int
	}{
		{"long path", `[1,2,3,4,5]`, strings.Repeat("p", 2000), 800},
		{"long number", "1" + strings.Repeat("0", 3000), "", 800},
		{"control chars", `"` + strings.Repeat(`\u0001`, 300) + `"`, "", 800},
	}
	for _, c := range cases {
		n, err := parseJSON([]byte(c.content))
		if err != nil {
			t.Fatal(err)
		}
		out := previewer{budget: c.budget, keep: keepSet(DefaultKeepKeys)}.fit(n, c.path).String()
		if len(out) > c.budget {
			t.Errorf("%s: %d bytes", c.name, len(out))
		}
	}
}

func TestPreviewCutsLongNumbers(t *testing.T) {
	c := "1" + strings.Repeat("0", 5000)
	root, _ := parseJSON([]byte(c))
	p := buildPreview(root, c, "res_n", 1500, keepSet(DefaultKeepKeys))
	if len(p) > 1500 || !json.Valid([]byte(p)) {
		t.Errorf("len %d", len(p))
	}
}

func TestPreviewDropsRootOffloadedKey(t *testing.T) {
	c := `{"_offloaded":1,"x":[` + strings.Repeat(`{"a":"bbbbbbbbbbbbbbbbbbbb"},`, 200) + `1]}`
	root, _ := parseJSON([]byte(c))
	p := buildPreview(root, c, "res_o", 1500, keepSet(DefaultKeepKeys))
	if _, ok := previewMeta(p); !ok {
		t.Errorf("not recognised: %s", p)
	}
}

func TestPreviewMetaIsStrict(t *testing.T) {
	for _, c := range []string{
		`{"_offloaded":{"ref":"res_abc"},"rows":[1,2]}`,
		`{"x":{"_offloaded":{"ref":"res_abc","chars":5,"read":"r"}},"y":1}`,
		`{"_offloaded":{"ref":"res_abc","chars":0,"read":"r"}}`,
		`{"_offloaded":{"ref":"res_abc","chars":5,"read":""}}`,
	} {
		if _, ok := previewMeta(c); ok {
			t.Errorf("previewMeta(%s) = true", c)
		}
	}
}

func kFields(n int) string {
	var kv []string
	for i := 0; i < n; i++ {
		kv = append(kv, fmt.Sprintf(`"k%d":%d`, i, i))
	}
	return strings.Join(kv, ",")
}

// The _more offset indexes all of the object's fields in document order, so
// the field at that index is the first k field the preview did not show.
func TestPreviewMoreOffsetIndexesAllFields(t *testing.T) {
	c := `{"value":"` + strings.Repeat("v", 1000) + `","notes":"short",` + kFields(100) + `}`
	for _, budget := range []int{1500, 3000} {
		root, _ := parseJSON([]byte(c))
		p := buildPreview(root, c, "res_o", budget, keepSet(DefaultKeepKeys))
		var got map[string]any
		if err := json.Unmarshal([]byte(p), &got); err != nil {
			t.Fatal(err)
		}
		var off int
		if _, err := fmt.Sscanf(got["_more"].(string)[strings.Index(got["_more"].(string), "offset="):], "offset=%d", &off); err != nil {
			t.Fatalf("budget %d: %v: %s", budget, err, p)
		}
		first := root.keys[off]
		if _, shown := got[first]; shown || !strings.HasPrefix(first, "k") {
			t.Errorf("budget %d: field at offset %d is %q (shown=%v)", budget, off, first, shown)
		}
		if _, shown := got[root.keys[off-1]]; !shown {
			t.Errorf("budget %d: field before offset %q not shown", budget, root.keys[off-1])
		}
	}
	c2 := "{" + kFields(2000) + "}"
	root, _ := parseJSON([]byte(c2))
	if p := buildPreview(root, c2, "res_o", 1500, keepSet(DefaultKeepKeys)); !strings.Contains(p, `offset=50`) {
		t.Errorf("plain root offset: %s", p)
	}
}

func TestPreviewHasOneMoreKey(t *testing.T) {
	c := `{"_more":"orig",` + kFields(100) + `}`
	root, _ := parseJSON([]byte(c))
	p := buildPreview(root, c, "res_o", 1500, keepSet(DefaultKeepKeys))
	if n := strings.Count(p, `"_more":`); n != 1 {
		t.Errorf("%d _more keys: %s", n, p)
	}
	if strings.Contains(p, "orig") {
		t.Errorf("own _more shown: %s", p)
	}
}

func TestPreviewMetaBailsOnHugeContent(t *testing.T) {
	c := `{"_offloaded":{"ref":"res_a","chars":5,"read":"r"},"x":"` + strings.Repeat("y", 250000) + `"}`
	if _, ok := previewMeta(c); ok {
		t.Error("huge content accepted")
	}
	if _, ok := previewMeta(`{"_offloaded":{"ref":"res_a","chars":5,"read":"r"}}` + "\n "); !ok {
		t.Error("trailing whitespace rejected")
	}
}
