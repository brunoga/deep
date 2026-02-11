package deep

import (
	"encoding/json"
	"testing"
)

func TestFieldConditionSerialization(t *testing.T) {
	type Data struct {
		A, B int
	}
	tests := []struct {
		name string
		cond Condition[Data]
	}{
		{"EqualField", EqualField[Data]("A", "B")},
		{"CompareField", CompareFieldCondition[Data]{Path1: "A", Path2: "B", Op: ">"}},
		{"AndCondition", And(EqualField[Data]("A", "B"), GreaterField[Data]("A", "B"))},
		{"OrCondition", Or(EqualField[Data]("A", "B"), GreaterField[Data]("A", "B"))},
		{"NotCondition", Not(EqualField[Data]("A", "B"))},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := json.Marshal(tt.cond)
			if err != nil {
				t.Fatalf("Marshal failed: %v", err)
			}
			cond2, err := unmarshalCondition[Data](data)
			if err != nil {
				t.Fatalf("Unmarshal failed: %v", err)
			}
			if cond2 == nil {
				t.Fatal("Expected non-nil condition")
			}
		})
	}
}

func TestPredicatesSerialization(t *testing.T) {
	type Config struct {
		Env string
	}
	type User struct {
		Name   string
		Age    int
		Tags   []string
		Config *Config
		Bio    string
	}

	tests := []struct {
		name string
		cond Condition[User]
	}{
		{"Defined", Defined[User]("Name")},
		{"Undefined", Undefined[User]("Missing")},
		{"Type", Type[User]("Name", "string")},
		{"Contains", Contains[User]("Bio", "Zurich")},
		{"ContainsFold", ContainsFold[User]("Bio", "zurich")},
		{"In", In[User]("Age", 20, 30)},
		{"InFold", InFold[User]("Name", "ALICE")},
		{"Log", Log[User]("test message")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := json.Marshal(tt.cond)
			if err != nil {
				t.Fatalf("Marshal failed: %v", err)
			}
			cond2, err := unmarshalCondition[User](data)
			if err != nil {
				t.Fatalf("Unmarshal failed: %v", err)
			}
			if cond2 == nil {
				t.Fatal("Expected non-nil condition")
			}
			// Test Log evaluation during serialization test to cover LogCondition.Evaluate
			if tt.name == "Log" {
				cond2.Evaluate(&User{Name: "foo"})
			}
		})
	}
}
