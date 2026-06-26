package taskmanager

import (
	"testing"
	"time"
)

func TestNewDispatcherDefaults(t *testing.T) {
	tm := &TaskManager{} // zero value is fine; we only inspect dispatcher config here
	d := NewDispatcher(tm)
	if d.tm != tm {
		t.Errorf("d.tm not set to the provided TaskManager")
	}
	if d.interval != time.Second {
		t.Errorf("default interval = %v, want 1s", d.interval)
	}
	if d.errHandler == nil {
		t.Errorf("default errHandler is nil, want non-nil no-op")
	}
	// no-op handler must be safe to call
	d.errHandler(nil)
}

func TestNewDispatcherOptions(t *testing.T) {
	tm := &TaskManager{}
	var got error
	d := NewDispatcher(tm,
		WithPollInterval(5*time.Millisecond),
		WithErrorHandler(func(err error) { got = err }),
	)
	if d.interval != 5*time.Millisecond {
		t.Errorf("interval = %v, want 5ms", d.interval)
	}
	sentinel := errSentinel{}
	d.errHandler(sentinel)
	if got != sentinel {
		t.Errorf("errHandler did not capture the error")
	}
}

type errSentinel struct{}

func (errSentinel) Error() string { return "sentinel" }

func TestNewDispatcherNilTaskManagerPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Errorf("NewDispatcher(nil) did not panic")
		}
	}()
	NewDispatcher(nil)
}

func TestNewDispatcherNonPositiveIntervalPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Errorf("WithPollInterval(0) did not panic")
		}
	}()
	NewDispatcher(&TaskManager{}, WithPollInterval(0))
}
