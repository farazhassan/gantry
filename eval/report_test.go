package eval_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/farazhassan/gantry"
	"github.com/farazhassan/gantry/eval"
)

func sampleReport() eval.Report {
	return eval.Report{
		Results: []eval.ScoredResult{
			{
				RunResult: eval.RunResult{Case: eval.Case{ID: "c1"}, Config: "cfg", FinalOutput: "yes", Duration: 100 * time.Millisecond, Usage: gantry.Usage{InputTokens: 50, OutputTokens: 10, Cost: 0.01}},
				Scores:    []eval.Score{{Name: "exact_match", Value: 1, Pass: true}},
			},
			{
				RunResult: eval.RunResult{Case: eval.Case{ID: "c2"}, Config: "cfg", FinalOutput: "no", Duration: 200 * time.Millisecond, Usage: gantry.Usage{InputTokens: 60, OutputTokens: 20, Cost: 0.02}},
				Scores:    []eval.Score{{Name: "exact_match", Value: 0, Pass: false}},
			},
		},
	}
}

func TestSummarizeProducesPerConfigStats(t *testing.T) {
	report := sampleReport()
	report.Summary = eval.SummarizeForTest(report.Results)

	s, ok := report.Summary["cfg"]
	if !ok {
		t.Fatalf("missing config summary")
	}
	if s.CaseCount != 2 {
		t.Errorf("CaseCount = %d, want 2", s.CaseCount)
	}
	if s.PassRate["exact_match"] != 0.5 {
		t.Errorf("PassRate = %v, want 0.5", s.PassRate["exact_match"])
	}
	if s.AvgDuration != 150*time.Millisecond {
		t.Errorf("AvgDuration = %v, want 150ms", s.AvgDuration)
	}
	if s.AvgTokens != 70.0 {
		t.Errorf("AvgTokens = %v, want 70", s.AvgTokens)
	}
}

func TestReportWriteJSON(t *testing.T) {
	report := sampleReport()
	report.Summary = eval.SummarizeForTest(report.Results)

	var buf bytes.Buffer
	if err := report.WriteJSON(&buf); err != nil {
		t.Fatalf("WriteJSON: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Errorf("invalid JSON: %v", err)
	}
}

func TestReportWriteCSV(t *testing.T) {
	report := sampleReport()
	report.Summary = eval.SummarizeForTest(report.Results)

	var buf bytes.Buffer
	if err := report.WriteCSV(&buf); err != nil {
		t.Fatalf("WriteCSV: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "config,case_id") {
		t.Errorf("CSV missing header; got %q", out)
	}
	if !strings.Contains(out, "c1") || !strings.Contains(out, "c2") {
		t.Errorf("CSV missing rows")
	}
}

func TestReportWriteSummary(t *testing.T) {
	report := sampleReport()
	report.Summary = eval.SummarizeForTest(report.Results)

	var buf bytes.Buffer
	if err := report.WriteSummary(&buf); err != nil {
		t.Fatalf("WriteSummary: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "cfg") || !strings.Contains(out, "exact_match") {
		t.Errorf("summary missing fields; got %q", out)
	}
}

// Verify that the eval Runner produces a Report whose Summary is populated.
func TestRunnerProducesSummary(t *testing.T) {
	runner := eval.Runner{
		Configs: []eval.Config{{Name: "cfg", Factory: func(ctx context.Context) (*gantry.Agent, error) {
			return gantry.NewAgent(gantry.WithLLM(eval.NewMockLLMClient(gantry.LLMResponse{Content: "x", StopReason: gantry.StopReasonEnd})))
		}}},
		Dataset: eval.SliceDataset{{ID: "c1"}},
		Scorers: []eval.Scorer{eval.ContainsScorer{Required: []string{"x"}}},
	}
	report, err := runner.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if _, ok := report.Summary["cfg"]; !ok {
		t.Errorf("Summary missing config 'cfg'; got %+v", report.Summary)
	}
}
