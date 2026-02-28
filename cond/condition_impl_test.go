package cond

import (
	"testing"
)

func TestApplyChecked_ExhaustiveConditions(t *testing.T) {
	type Data struct {
		V int
		S string
		B bool
	}
	tests := []struct {
		name string
		expr string
		v    Data
		want bool
	}{
		{"Equal", "V == 10", Data{V: 10}, true},
		{"NotEqual", "V != 10", Data{V: 5}, true},
		{"Greater", "V > 5", Data{V: 10}, true},
		{"GreaterFalse", "V > 10", Data{V: 10}, false},
		{"Less", "V < 10", Data{V: 5}, true},
		{"GreaterEqual", "V >= 10", Data{V: 10}, true},
		{"LessEqual", "V <= 10", Data{V: 10}, true},
		{"StringGreater", "S > 'a'", Data{S: "b"}, true},
		{"AndTrue", "V > 0 AND B == true", Data{V: 1, B: true}, true},
		{"AndFalse", "V > 0 AND B == false", Data{V: 1, B: true}, false},
		{"OrTrue", "V > 10 OR B == true", Data{V: 1, B: true}, true},
		{"NotTrue", "NOT (V == 0)", Data{V: 1}, true},
		{"Complex", "(V > 0 AND V < 10) OR V == 100", Data{V: 5}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cond, err := ParseCondition[Data](tt.expr)
			if err != nil {
				t.Fatalf("ParseCondition failed: %v", err)
			}
			got, err := cond.Evaluate(&tt.v)
			if err != nil {
				t.Fatalf("Evaluate failed: %v", err)
			}
			if got != tt.want {
				t.Errorf("Evaluate() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestFieldConditions(t *testing.T) {
	type Data struct {
		A int
		B int
		C int
		S string
		T string
	}
	tests := []struct {
		name string
		cond Condition[Data]
		v    Data
		want bool
	}{
		{"EqualField_True", EqualField[Data]("A", "B"), Data{A: 1, B: 1}, true},
		{"EqualField_False", EqualField[Data]("A", "B"), Data{A: 1, B: 2}, false},
		{"NotEqualField_True", NotEqualField[Data]("A", "C"), Data{A: 1, C: 2}, true},
		{"NotEqualField_False", NotEqualField[Data]("A", "B"), Data{A: 1, B: 1}, false},
		{"GreaterField_True", GreaterField[Data]("B", "A"), Data{A: 1, B: 2}, true},
		{"GreaterField_False", GreaterField[Data]("A", "B"), Data{A: 1, B: 2}, false},
		{"LessField_True", LessField[Data]("A", "C"), Data{A: 1, C: 2}, true},
		{"LessEqualField_True", LessEqualField[Data]("A", "B"), Data{A: 1, B: 1}, true},
		{"GreaterEqualField_True", GreaterEqualField[Data]("A", "B"), Data{A: 1, B: 1}, true},
		{"String_LessField", LessField[Data]("S", "T"), Data{S: "apple", T: "banana"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.cond.Evaluate(&tt.v)
			if err != nil {
				t.Fatalf("Evaluate failed: %v", err)
			}
			if got != tt.want {
				t.Errorf("Evaluate() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestNewPredicates(t *testing.T) {
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

	u := &User{
		Name: "Alice",
		Age:  30,
		Tags: []string{"admin", "user"},
		Config: &Config{
			Env: "prod",
		},
		Bio: "Software Engineer from Zurich",
	}

	tests := []struct {
		name string
		cond Condition[User]
		want bool
	}{
		{"Defined_Name", Defined[User]("Name"), true},
		{"Defined_Age", Defined[User]("Age"), true},
		{"Defined_Missing", Defined[User]("Missing"), false},
		{"Undefined_Missing", Undefined[User]("Missing"), true},
		{"Undefined_Name", Undefined[User]("Name"), false},
		{"Type_Name_String", Type[User]("Name", "string"), true},
		{"Type_Age_Number", Type[User]("Age", "number"), true},
		{"Type_Tags_Array", Type[User]("Tags", "array"), true},
		{"Type_Config_Object", Type[User]("Config", "object"), true},
		{"Type_Bio_String", Type[User]("Bio", "string"), true},
		{"Contains_Bio", Contains[User]("Bio", "Zurich"), true},
		{"Contains_Bio_Fold", ContainsFold[User]("Bio", "zurich"), true},
		{"Contains_Bio_False", Contains[User]("Bio", "Berlin"), false},
		{"StartsWith_Bio", StartsWith[User]("Bio", "Software"), true},
		{"StartsWith_Bio_Fold", StartsWithFold[User]("Bio", "software"), true},
		{"EndsWith_Bio", EndsWith[User]("Bio", "Zurich"), true},
		{"EndsWith_Bio_Fold", EndsWithFold[User]("Bio", "zurich"), true},
		{"Matches_Bio", Matches[User]("Bio", ".*Engineer.*"), true},
		{"Matches_Bio_Fold", MatchesFold[User]("Bio", ".*engineer.*"), true},
		{"In_Age", In[User]("Age", 20, 30, 40), true},
		{"In_Age_False", In[User]("Age", 20, 25, 40), false},
		{"In_Bio_Fold", InFold[User]("Name", "ALICE", "BOB"), true},
		{"EqFold", EqFold[User]("Name", "alice"), true},
		{"NeFold", NeFold[User]("Name", "bob"), true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.cond.Evaluate(u)
			if err != nil {
				t.Fatalf("Evaluate failed: %v", err)
			}
			if got != tt.want {
				t.Errorf("Evaluate() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCompareValues_Exhaustive(t *testing.T) {
	type Data struct {
		U uint
		F float64
		S string
	}
	tests := []struct {
		name string
		expr string
		v    Data
		want bool
	}{
		{"U_>_5", "U > 5", Data{U: 10}, true},
		{"U_<_20", "U < 20", Data{U: 10}, true},
		{"U_>=_10", "U >= 10", Data{U: 10}, true},
		{"U_<=_10", "U <= 10", Data{U: 10}, true},
		{"F_>_3.0", "F > 3.0", Data{F: 3.14}, true},
		{"F_<_4.0", "F < 4.0", Data{F: 3.14}, true},
		{"S_>_'apple'", "S > 'apple'", Data{S: "banana"}, true},
		{"S_<_'cherry'", "S < 'cherry'", Data{S: "banana"}, true},
		{"S_==_'banana'", "S == 'banana'", Data{S: "banana"}, true},
		{"S_!=_'apple'", "S != 'apple'", Data{S: "banana"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cond, err := ParseCondition[Data](tt.expr)
			if err != nil {
				t.Fatalf("ParseCondition failed: %v", err)
			}
			got, err := cond.Evaluate(&tt.v)
			if err != nil {
				t.Fatalf("Evaluate failed: %v", err)
			}
			if got != tt.want {
				t.Errorf("Evaluate() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCondition_Aliases(t *testing.T) {
	type Data struct {
		I int
		S string
	}
	d := Data{I: 10, S: "FOO"}

	tests := []struct {
		name string
		cond Condition[Data]
		want bool
	}{
		{"Ne", Ne[Data]("I", 11), true},
		{"NeFold", NeFold[Data]("S", "bar"), true},
		{"GreaterEqual", GreaterEqual[Data]("I", 10), true},
		{"LessEqual", LessEqual[Data]("I", 10), true},
		{"EqualFieldFold", EqualFieldFold[Data]("S", "S"), true},
	}

	for _, tt := range tests {
		got, _ := tt.cond.Evaluate(&d)
		if got != tt.want {
			t.Errorf("%s: got %v, want %v", tt.name, got, tt.want)
		}
	}
}
