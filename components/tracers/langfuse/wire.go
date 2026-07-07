package langfuse

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"time"

	"github.com/farazhassan/gantry"
)

const timeFormat = time.RFC3339Nano

// ingestionItem is one envelope in a Langfuse ingestion batch. ID is the
// envelope's idempotency key (distinct from any id inside Body).
type ingestionItem struct {
	ID        string         `json:"id"`
	Type      string         `json:"type"`
	Timestamp string         `json:"timestamp"`
	Body      map[string]any `json:"body"`
}

// ingestionBatch is the top-level request body for POST /api/public/ingestion.
type ingestionBatch struct {
	Batch []ingestionItem `json:"batch"`
}

// newID returns a random hex id. Falls back to a timestamp if the system RNG
// is unavailable, matching the default in-memory tracer.
func newID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return time.Now().Format("20060102150405.000000000")
	}
	return hex.EncodeToString(b[:])
}

// nowStamp is the ingestion-event timestamp (when the envelope was created).
// This is intentionally distinct from a span/event's own start time carried in
// Body; do not collapse the two.
func nowStamp() string { return time.Now().UTC().Format(timeFormat) }

func traceCreateItem(traceID, name string, start time.Time) ingestionItem {
	return ingestionItem{
		ID:        newID(),
		Type:      "trace-create",
		Timestamp: nowStamp(),
		Body: map[string]any{
			"id":        traceID,
			"name":      name,
			"timestamp": start.UTC().Format(timeFormat),
		},
	}
}

func spanCreateItem(traceID, spanID, parentID, name string, start, end time.Time, attrs map[string]any, err error) ingestionItem {
	body := map[string]any{
		"id":        spanID,
		"traceId":   traceID,
		"name":      name,
		"startTime": start.UTC().Format(timeFormat),
		"endTime":   end.UTC().Format(timeFormat),
	}
	if parentID != "" {
		body["parentObservationId"] = parentID
	}
	if len(attrs) > 0 {
		body["metadata"] = attrs
	}
	if err != nil {
		body["level"] = "ERROR"
		body["statusMessage"] = err.Error()
	}
	return ingestionItem{
		ID:        newID(),
		Type:      "span-create",
		Timestamp: nowStamp(),
		Body:      body,
	}
}

func eventCreateItem(traceID, parentID, name string, start time.Time, attrs map[string]any) ingestionItem {
	// Events are leaf observations: nothing parents off them, so eventCreateItem
	// generates the observation id itself rather than taking one from the caller
	// (unlike spanCreateItem, whose id is the reusable Gantry span id).
	body := map[string]any{
		"id":        newID(),
		"traceId":   traceID,
		"name":      name,
		"startTime": start.UTC().Format(timeFormat),
	}
	if parentID != "" {
		body["parentObservationId"] = parentID
	}
	if len(attrs) > 0 {
		body["metadata"] = attrs
	}
	return ingestionItem{
		ID:        newID(),
		Type:      "event-create",
		Timestamp: nowStamp(),
		Body:      body,
	}
}

// generationCreateItem builds a Langfuse generation observation from a gantry
// SpanKindGeneration span. It promotes the neutral generation attributes
// (model, input, output, usage_in/out) into Langfuse-native fields and leaves
// everything else — minus the kind marker — in metadata.
func generationCreateItem(traceID, spanID, parentID, name string, start, end time.Time, attrs map[string]any, err error) ingestionItem {
	body := map[string]any{
		"id":        spanID,
		"traceId":   traceID,
		"name":      name,
		"startTime": start.UTC().Format(timeFormat),
		"endTime":   end.UTC().Format(timeFormat),
	}
	if parentID != "" {
		body["parentObservationId"] = parentID
	}

	// Copy attrs so promotion/stripping never mutates the caller's map.
	md := make(map[string]any, len(attrs))
	for k, v := range attrs {
		md[k] = v
	}
	delete(md, gantry.SpanKindKey)

	if m, ok := md[gantry.AttrModel].(string); ok && m != "" {
		body["model"] = m
		delete(md, gantry.AttrModel)
	}
	if in, ok := md[gantry.AttrInput]; ok {
		body["input"] = decodeJSON(in)
		delete(md, gantry.AttrInput)
	}
	if out, ok := md[gantry.AttrOutput]; ok {
		body["output"] = decodeJSON(out)
		delete(md, gantry.AttrOutput)
	}
	usageIn, okIn := md[gantry.AttrUsageIn]
	usageOut, okOut := md[gantry.AttrUsageOut]
	if okIn || okOut {
		usage := map[string]any{"unit": "TOKENS"}
		if okIn {
			usage["input"] = usageIn
			delete(md, gantry.AttrUsageIn)
		}
		if okOut {
			usage["output"] = usageOut
			delete(md, gantry.AttrUsageOut)
		}
		body["usage"] = usage
	}
	if len(md) > 0 {
		body["metadata"] = md
	}
	if err != nil {
		body["level"] = "ERROR"
		body["statusMessage"] = err.Error()
	}
	return ingestionItem{
		ID:        newID(),
		Type:      "generation-create",
		Timestamp: nowStamp(),
		Body:      body,
	}
}

// decodeJSON parses a JSON-encoded attribute back into a value so Langfuse
// renders it structured (e.g. a chat transcript) instead of as a quoted string.
// On any error — or a non-string input — it returns the value unchanged, so a
// non-JSON attribute still round-trips safely.
func decodeJSON(v any) any {
	s, ok := v.(string)
	if !ok {
		return v
	}
	var out any
	if err := json.Unmarshal([]byte(s), &out); err != nil {
		return s
	}
	return out
}
