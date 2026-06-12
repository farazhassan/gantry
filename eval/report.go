package eval

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strconv"
	"time"
)

// Report is the full output of one eval run.
type Report struct {
	Results []ScoredResult           `json:"results"`
	Summary map[string]ConfigSummary `json:"summary"`
}

// ConfigSummary aggregates per-config metrics across cases.
type ConfigSummary struct {
	Config      string             `json:"config"`
	CaseCount   int                `json:"case_count"`
	Errors      int                `json:"errors"`
	PassRate    map[string]float64 `json:"pass_rate"`
	MeanScore   map[string]float64 `json:"mean_score"`
	P50         map[string]float64 `json:"p50"`
	P95         map[string]float64 `json:"p95"`
	AvgDuration time.Duration      `json:"avg_duration"`
	AvgTokens   float64            `json:"avg_tokens"`
	AvgCost     float64            `json:"avg_cost"`
}

// summarize groups results by config and computes statistics.
func summarize(results []ScoredResult) map[string]ConfigSummary {
	byCfg := map[string][]ScoredResult{}
	for _, r := range results {
		byCfg[r.Config] = append(byCfg[r.Config], r)
	}
	out := map[string]ConfigSummary{}
	for cfg, rs := range byCfg {
		out[cfg] = aggregateForConfig(cfg, rs)
	}
	return out
}

// SummarizeForTest is an exported alias used by tests; mirrors summarize.
func SummarizeForTest(results []ScoredResult) map[string]ConfigSummary {
	return summarize(results)
}

func aggregateForConfig(cfg string, rs []ScoredResult) ConfigSummary {
	s := ConfigSummary{
		Config:    cfg,
		CaseCount: len(rs),
		PassRate:  map[string]float64{},
		MeanScore: map[string]float64{},
		P50:       map[string]float64{},
		P95:       map[string]float64{},
	}

	var totalDur time.Duration
	var totalTokens, totalCost float64
	var byScorer = map[string][]float64{}
	var passes = map[string]int{}

	for _, r := range rs {
		if r.Err != nil {
			s.Errors++
		}
		totalDur += r.Duration
		totalTokens += float64(r.Usage.InputTokens + r.Usage.OutputTokens)
		totalCost += r.Usage.Cost
		for _, sc := range r.Scores {
			byScorer[sc.Name] = append(byScorer[sc.Name], sc.Value)
			if sc.Pass {
				passes[sc.Name]++
			}
		}
	}

	if len(rs) > 0 {
		s.AvgDuration = totalDur / time.Duration(len(rs))
		s.AvgTokens = totalTokens / float64(len(rs))
		s.AvgCost = totalCost / float64(len(rs))
	}

	for name, vs := range byScorer {
		s.PassRate[name] = float64(passes[name]) / float64(len(rs))
		s.MeanScore[name] = mean(vs)
		sorted := append([]float64(nil), vs...)
		sort.Float64s(sorted)
		s.P50[name] = percentile(sorted, 0.50)
		s.P95[name] = percentile(sorted, 0.95)
	}
	return s
}

func mean(vs []float64) float64 {
	if len(vs) == 0 {
		return 0
	}
	var sum float64
	for _, v := range vs {
		sum += v
	}
	return sum / float64(len(vs))
}

func percentile(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	idx := int(float64(len(sorted)-1) * p)
	if idx < 0 {
		idx = 0
	}
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}

// WriteJSON writes the full Report as pretty JSON.
func (r Report) WriteJSON(w io.Writer) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(r)
}

// WriteCSV writes one row per ScoredResult with flat fields per scorer.
// Columns: config, case_id, final_output, duration_ms, tokens, cost, err, <scorer_name>_pass, <scorer_name>_value
func (r Report) WriteCSV(w io.Writer) error {
	scorerNames := map[string]struct{}{}
	for _, res := range r.Results {
		for _, sc := range res.Scores {
			scorerNames[sc.Name] = struct{}{}
		}
	}
	names := make([]string, 0, len(scorerNames))
	for n := range scorerNames {
		names = append(names, n)
	}
	sort.Strings(names)

	header := []string{"config", "case_id", "final_output", "duration_ms", "tokens", "cost", "err"}
	for _, n := range names {
		header = append(header, n+"_pass", n+"_value")
	}

	cw := csv.NewWriter(w)
	if err := cw.Write(header); err != nil {
		return err
	}
	for _, res := range r.Results {
		errStr := ""
		if res.Err != nil {
			errStr = res.Err.Error()
		}
		row := []string{
			res.Config,
			res.Case.ID,
			res.FinalOutput,
			strconv.FormatInt(res.Duration.Milliseconds(), 10),
			strconv.Itoa(res.Usage.InputTokens + res.Usage.OutputTokens),
			strconv.FormatFloat(res.Usage.Cost, 'f', 4, 64),
			errStr,
		}
		scoreByName := map[string]Score{}
		for _, sc := range res.Scores {
			scoreByName[sc.Name] = sc
		}
		for _, n := range names {
			sc := scoreByName[n]
			row = append(row,
				strconv.FormatBool(sc.Pass),
				strconv.FormatFloat(sc.Value, 'f', 4, 64),
			)
		}
		if err := cw.Write(row); err != nil {
			return err
		}
	}
	cw.Flush()
	return cw.Error()
}

// WriteSummary writes a human-readable text summary table to w.
func (r Report) WriteSummary(w io.Writer) error {
	cfgs := make([]string, 0, len(r.Summary))
	for c := range r.Summary {
		cfgs = append(cfgs, c)
	}
	sort.Strings(cfgs)
	for _, c := range cfgs {
		s := r.Summary[c]
		fmt.Fprintf(w, "config: %s  cases: %d  errors: %d  avg_dur: %v  avg_tokens: %.1f  avg_cost: %.4f\n",
			s.Config, s.CaseCount, s.Errors, s.AvgDuration, s.AvgTokens, s.AvgCost)
		names := make([]string, 0, len(s.PassRate))
		for n := range s.PassRate {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			fmt.Fprintf(w, "  %s  pass_rate=%.2f  mean=%.3f  p50=%.3f  p95=%.3f\n",
				n, s.PassRate[n], s.MeanScore[n], s.P50[n], s.P95[n])
		}
	}
	return nil
}
