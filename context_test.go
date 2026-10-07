package gantry_test

import (
	"testing"

	"github.com/farazhassan/gantry"
)

func TestUsageAddSumsCacheFields(t *testing.T) {
	a := gantry.Usage{InputTokens: 10, OutputTokens: 2, CacheReadTokens: 6, CacheWriteTokens: 1, Cost: 0.5}
	b := gantry.Usage{InputTokens: 5, OutputTokens: 3, CacheReadTokens: 4, CacheWriteTokens: 2, Cost: 0.25}
	got := a.Add(b)
	want := gantry.Usage{InputTokens: 15, OutputTokens: 5, CacheReadTokens: 10, CacheWriteTokens: 3, Cost: 0.75}
	if got != want {
		t.Errorf("Add = %+v, want %+v", got, want)
	}
}
