// Package conformance contains shared test suites that downstream adapter
// authors can use to verify their implementations satisfy the gantry
// interface contracts. Each suite takes a *testing.T and a factory that
// returns a fresh implementation, and runs a battery of contract checks.
//
// Example usage (in an adapter's test file):
//
//	func TestMyTranscriptConformance(t *testing.T) {
//	    conformance.TranscriptSuite(t, func() transcript.Transcript {
//	        return mypkg.NewTranscript(...)
//	    })
//	}
package conformance
