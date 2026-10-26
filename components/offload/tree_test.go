package offload

import (
	"strings"
	"testing"
)

func TestParseKeepsKeyOrderAndRoundTrips(t *testing.T) {
	in := `{"b":1,"a":[true,null,"x"],"c":{"z":2.5,"y":"q"}}`
	n, err := parseJSON([]byte(in))
	if err != nil {
		t.Fatalf("parseJSON: %v", err)
	}
	if got := n.String(); got != in {
		t.Errorf("round trip = %s, want %s", got, in)
	}
}

func TestParseDecodesNestedJSONStrings(t *testing.T) {
	in := `{"output":"{\"items\":[{\"src\":\"mtr_1\",\"result\":{\"value\":3}}]}"}`
	n, err := parseJSON([]byte(in))
	if err != nil {
		t.Fatalf("parseJSON: %v", err)
	}
	v, err := n.at("output.items[0].result.value")
	if err != nil {
		t.Fatalf("at: %v", err)
	}
	if v.String() != "3" {
		t.Errorf("value = %s, want 3", v.String())
	}
}

func TestParseKeepsPlainStringsAndRejectsTrailingData(t *testing.T) {
	n, err := parseJSON([]byte(`{"s":"{not json","u":"<b>&"}`))
	if err != nil {
		t.Fatalf("parseJSON: %v", err)
	}
	if got := n.String(); got != `{"s":"{not json","u":"<b>&"}` {
		t.Errorf("String = %s", got)
	}
	if _, err := parseJSON([]byte(`{"a":1} {"b":2}`)); err == nil {
		t.Error("parseJSON accepted trailing data")
	}
	if _, err := parseJSON([]byte(`plain text`)); err == nil {
		t.Error("parseJSON accepted non-JSON")
	}
}

func TestAtReportsMissingPaths(t *testing.T) {
	n, _ := parseJSON([]byte(`{"a":[{"b":1}]}`))
	for _, p := range []string{"x", "a[1]", "a[0].c", "a.b", "a[x]", "a[0"} {
		if _, err := n.at(p); err == nil {
			t.Errorf("at(%q): want error", p)
		}
	}
	if v, err := n.at(""); err != nil || v != n {
		t.Errorf(`at("") = %v, %v; want the root`, v, err)
	}
}

func TestSrcPathsFindsEveryHandleOnceInOrder(t *testing.T) {
	in := `{"src":"mtr_a","output":"{\"items\":[{\"src\":\"mtr_b\"},{\"src\":\"mtr_a\"},{\"src\":\"qry_c\"}]}"}`
	n, _ := parseJSON([]byte(in))
	got := srcPaths(n)
	want := []srcPath{{"mtr_a", ""}, {"mtr_b", "output.items[0]"}, {"qry_c", "output.items[2]"}}
	if len(got) != len(want) {
		t.Fatalf("srcPaths = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("srcPaths[%d] = %v, want %v", i, got[i], want[i])
		}
	}
}

func TestJoinPaths(t *testing.T) {
	cases := [][3]string{{"", "a", "a"}, {"a", "", "a"}, {"a", "b", "a.b"}, {"a", "[2]", "a[2]"}, {"", "[0].x", "[0].x"}}
	for _, c := range cases {
		if got := joinPaths(c[0], c[1]); got != c[2] {
			t.Errorf("joinPaths(%q,%q) = %q, want %q", c[0], c[1], got, c[2])
		}
	}
}

func TestSrcPathsRoundTripAwkwardKeys(t *testing.T) {
	for _, in := range []string{
		`{"x.y":{"src":"s1"},"a[0]":{"src":"s2"},"":{"src":"s3"},"q\"z":{"src":"s4"},"n":{"":{"src":"s5"},"b.c":[{"src":"s6"}]},"src":"r"}`,
		`{"":{"src":"s3"},"src":"r"}`,
	} {
		n, err := parseJSON([]byte(in))
		if err != nil {
			t.Fatal(err)
		}
		sps := srcPaths(n)
		if len(sps) < 2 {
			t.Fatalf("srcPaths = %v", sps)
		}
		for _, sp := range sps {
			v, err := n.at(sp.path)
			if err != nil {
				t.Errorf("at(%q): %v", sp.path, err)
				continue
			}
			if s, _ := v.get("src").str(); s != sp.src {
				t.Errorf("at(%q).src = %q, want %q", sp.path, s, sp.src)
			}
		}
	}
	n, _ := parseJSON([]byte(`{"x.y":{"src":"s1"}}`))
	if v, err := n.at(`["x.y"].src`); err != nil || v.String() != `"s1"` {
		t.Errorf(`at(["x.y"].src) = %v, %v`, v, err)
	}
}

func TestParseCapsNestingDepth(t *testing.T) {
	deep := func(n int) []byte { return []byte(strings.Repeat("[", n) + strings.Repeat("]", n)) }
	if _, err := parseJSON(deep(10001)); err == nil {
		t.Error("parseJSON accepted 10001 levels")
	}
	if _, err := parseJSON(deep(100)); err != nil {
		t.Errorf("depth 100: %v", err)
	}
}
