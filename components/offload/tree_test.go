package offload

import "testing"

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
