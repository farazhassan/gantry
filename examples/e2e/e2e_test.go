package main

import (
	"context"
	"testing"
)

func TestRunExampleCompletesSuccessfully(t *testing.T) {
	if err := RunExample(context.Background()); err != nil {
		t.Fatalf("RunExample: %v", err)
	}
}
