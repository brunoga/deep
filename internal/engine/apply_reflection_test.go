package engine

import (
	"reflect"
	"testing"
)

// TestApplyOpReflectionValueNilLogger asserts the value-variant entry point
// doesn't panic when a caller passes a nil logger and an OpLog op forces a
// log call. The pointer variant has always nil-checked the logger; the value
// variant must match so external callers (including future generated code)
// don't NPE on logger.Info.
func TestApplyOpReflectionValueNilLogger(t *testing.T) {
	type S struct{ A int }
	s := S{A: 1}
	v := reflect.ValueOf(&s).Elem()

	defer func() {
		if r := recover(); r != nil {
			t.Errorf("nil logger caused panic: %v", r)
		}
	}()

	if err := ApplyOpReflectionValue(v, Operation{Kind: OpLog, Path: "/", New: "msg"}, nil); err != nil {
		t.Errorf("OpLog with nil logger should succeed silently, got err=%v", err)
	}
}
