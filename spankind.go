package gantry

// SpanKindKey is the well-known span attribute under which a gantry primitive
// declares its kind. Exporters read it to map a span onto their own observation
// model (e.g. the Langfuse adapter renders SpanKindGeneration as a generation).
// It is exporter-neutral: gantry does not bind to any vendor's conventions.
const SpanKindKey = "gantry.kind"

// Span kinds label a span with the gantry primitive that opened it.
const (
	SpanKindAgent      = "agent"      // the run-level span
	SpanKindPhase      = "phase"      // one phase of the loop
	SpanKindGeneration = "generation" // one LLM call
	SpanKindTask       = "task"       // one task drive-cycle
)

// Generation attribute keys. A primitive that opens a SpanKindGeneration span
// records these on it; exporters promote them into their native generation
// fields (e.g. Langfuse model/input/output/usage). Kept as plain attributes so
// any Tracer that ignores them still works.
const (
	AttrModel    = "model"
	AttrInput    = "input"
	AttrOutput   = "output"
	AttrUsageIn  = "usage_in"
	AttrUsageOut = "usage_out"
)
