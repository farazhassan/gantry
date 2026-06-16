package main

import "testing"

func TestStubFSTools_NamespacedAggregation(t *testing.T) {
	tools := newStubFSTools(t)
	if len(tools) != 2 {
		t.Fatalf("want 2 tools, got %d", len(tools))
	}
	names := map[string]bool{}
	for _, tl := range tools {
		names[tl.Definition().Name] = true
	}
	if !names["fs__read_file"] || !names["fs__write_file"] {
		t.Fatalf("want fs__read_file and fs__write_file, got %v", names)
	}
}

func TestDefaultServerConfigs(t *testing.T) {
	cfgs := defaultServerConfigs("/tmp/sandbox")
	if len(cfgs) != 3 {
		t.Fatalf("want 3 server configs (fs/web/time), got %d", len(cfgs))
	}
	for _, sc := range cfgs {
		if sc.Namespace == "" || sc.Config.Command == "" {
			t.Fatalf("incomplete server config: %#v", sc)
		}
	}
}
