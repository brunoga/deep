package deep

import (
	"testing"
)

func TestOptions(t *testing.T) {
	// Verify options implement interfaces
	var _ CopyOption = SkipUnsupported()
	var _ CopyOption = IgnorePath("A")
	var _ EqualOption = IgnorePath("A")
	var _ DiffOption = IgnorePath("A")
	var _ DiffOption = DiffDetectMoves(true)
	
	// Copy option
	skip := SkipUnsupported()
	if skip == nil {
		t.Error("SkipUnsupported returned nil")
	}
	
	// IgnorePath
	ignore := IgnorePath("A")
	if ignore == nil {
		t.Error("IgnorePath returned nil")
	}
}
