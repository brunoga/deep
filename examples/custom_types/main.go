package main

import (
	"fmt"
	"github.com/brunoga/deep/v5"
	"time"
)

// CustomTime wraps time.Time to provide specialized diffing.
type CustomTime struct {
	time.Time
}

// Diff implements custom diffing logic for CustomTime.
func (t CustomTime) Diff(other *CustomTime) v5.Patch[CustomTime] {
	if t.Time.Equal(other.Time) {
		return v5.Patch[CustomTime]{}
	}
	return v5.Patch[CustomTime]{
		Operations: []v5.Operation{{
			Kind: v5.OpReplace,
			Path: "",
			Old:  t.Format(time.Kitchen),
			New:  other.Format(time.Kitchen),
		}},
	}
}

func (t *CustomTime) ApplyOperation(op v5.Operation) (bool, error) {
	if op.Path == "" || op.Path == "/" {
		if op.Kind == v5.OpReplace {
			if s, ok := op.New.(string); ok {
				// In a real app, we'd parse the time back
				fmt.Printf("Applying custom time: %v\n", s)
				parsed, _ := time.Parse(time.Kitchen, s)
				t.Time = parsed
				return true, nil
			}
		}
	}
	return false, fmt.Errorf("invalid operation for CustomTime: %s", op.Path)
}

func (t CustomTime) Equal(other *CustomTime) bool {
	return t.Time.Equal(other.Time)
}

func (t CustomTime) Copy() *CustomTime {
	return &CustomTime{t.Time}
}

type Event struct {
	Name string     `json:"name"`
	When CustomTime `json:"when"`
}

func main() {
	now := time.Now()
	e1 := Event{Name: "Meeting", When: CustomTime{now}}
	e2 := Event{Name: "Meeting", When: CustomTime{now.Add(1 * time.Hour)}}

	patch := v5.Diff(e1, e2)

	fmt.Println("--- COMPARING WITH CUSTOM DIFF LOGIC ---")
	for _, op := range patch.Operations {
		fmt.Printf("Change at %s: %v -> %v\n", op.Path, op.Old, op.New)
	}
}
