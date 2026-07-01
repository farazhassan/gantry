package checkpointer

import (
	"encoding/json"
	"reflect"

	"github.com/farazhassan/gantry"
)

// Field names a top-level field of gantry.State by its JSON key. State carries no
// JSON tags, so the key equals the Go field name (guarded by a test).
type Field string

const (
	FieldInput            Field = "Input"
	FieldTask             Field = "Task"
	FieldSystem           Field = "System"
	FieldMessages         Field = "Messages"
	FieldTools            Field = "Tools"
	FieldRetrieved        Field = "Retrieved"
	FieldPlan             Field = "Plan"
	FieldIteration        Field = "Iteration"
	FieldLastResponse     Field = "LastResponse"
	FieldPendingToolCalls Field = "PendingToolCalls"
	FieldToolResults      Field = "ToolResults"
	FieldDone             Field = "Done"
	FieldDoneReason       Field = "DoneReason"
	FieldFinalOutput      Field = "FinalOutput"
	FieldTrace            Field = "Trace"
	FieldUsage            Field = "Usage"
	FieldMeta             Field = "Meta"
)

// AllFields returns every Field constant (used to validate coverage of State).
func AllFields() []Field {
	return []Field{
		FieldInput, FieldTask, FieldSystem, FieldMessages, FieldTools, FieldRetrieved,
		FieldPlan, FieldIteration, FieldLastResponse, FieldPendingToolCalls,
		FieldToolResults, FieldDone, FieldDoneReason, FieldFinalOutput, FieldTrace,
		FieldUsage, FieldMeta,
	}
}

type projMode int

const (
	projNone projMode = iota
	projOnly
	projOmit
)

type projection struct {
	mode   projMode
	fields map[Field]bool
}

// Option configures a StoreCheckpointer.
type Option func(*config)

type config struct {
	proj     projection
	projOpts int // count of projection options applied (>1 ⇒ conflicting)
}

// StoreOnly persists only the listed State fields; all others are dropped.
func StoreOnly(fields ...Field) Option {
	return func(c *config) {
		c.proj = projection{mode: projOnly, fields: fieldSet(fields)}
		c.projOpts++
	}
}

// Omit persists all State fields except the listed ones.
func Omit(fields ...Field) Option {
	return func(c *config) {
		c.proj = projection{mode: projOmit, fields: fieldSet(fields)}
		c.projOpts++
	}
}

func fieldSet(fields []Field) map[Field]bool {
	m := make(map[Field]bool, len(fields))
	for _, f := range fields {
		m[f] = true
	}
	return m
}

// marshal serializes state, including only the projected fields. For projNone it
// marshals the whole State. For projOnly/projOmit it marshals ONLY the kept
// fields (so dropped fields — e.g. a populated Trace — are never serialized).
func (p projection) marshal(state *gantry.State) ([]byte, error) {
	if p.mode == projNone {
		return json.Marshal(state)
	}
	v := reflect.ValueOf(*state)
	rt := v.Type()
	out := make(map[string]json.RawMessage, rt.NumField())
	for i := 0; i < rt.NumField(); i++ {
		name := rt.Field(i).Name
		keep := p.fields[Field(name)]
		if p.mode == projOmit {
			keep = !keep
		}
		if !keep {
			continue
		}
		raw, err := json.Marshal(v.Field(i).Interface())
		if err != nil {
			return nil, err
		}
		out[name] = raw
	}
	return json.Marshal(out)
}
