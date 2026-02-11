package deep

import (
	"testing"
)

func TestParseCondition(t *testing.T) {
	type Data struct {
		Name   string
		Level  int
		Active bool
		Score  float64
		Tags   []string
	}
	tests := []string{
		"Name == 'Alice'",
		"Level > 5",
		"Active == true",
		"Score >= 95.0",
		"Tags[0] == 'admin'",
		"(Level > 5 AND Active == true) OR Name == 'Bob'",
		"NOT (Level < 5)",
		"Level > 5 AND Level < 15",
		"Level == 10 OR Level == 20",
		"Name != 'Bob' AND (Score < 100.0 OR Level == 0)",
	}
	for _, tt := range tests {
		t.Run(tt, func(t *testing.T) {
			cond, err := ParseCondition[Data](tt)
			if err != nil {
				t.Fatalf("ParseCondition failed: %v", err)
			}
			if cond == nil {
				t.Fatal("Expected non-nil condition")
			}
		})
	}
}

func TestParseFieldCondition(t *testing.T) {
	type Data struct {
		A, B, C int
	}
	tests := []string{
		"A == B",
		"A != C",
		"C > A",
		"A < C",
		"A >= B",
		"A <= B",
		"A == C",
	}
	for _, tt := range tests {
		t.Run(tt, func(t *testing.T) {
			cond, err := ParseCondition[Data](tt)
			if err != nil {
				t.Fatalf("ParseCondition failed: %v", err)
			}
			if cond == nil {
				t.Fatal("Expected non-nil condition")
			}
		})
	}
}
