package deep

import (
	"encoding/json"
	"testing"
)

func TestNewPredicates(t *testing.T) {
	type User struct {
		Name   *string
		Age    int
		Tags   []string
		Config map[string]string
		Bio    string
	}

	alice := "Alice"
	u := User{
		Name: &alice,
		Age:  30,
		Tags: []string{"admin", "editor"},
		Config: map[string]string{
			"theme": "dark",
		},
		Bio: "Hello, I am Alice.",
	}

	tests := []struct {
		name string
		cond Condition[User]
		want bool
	}{
		// Defined / Undefined
		{"Defined_Name", Defined[User]("Name"), true},
		{"Defined_Age", Defined[User]("Age"), true},
		{"Defined_Missing", Defined[User]("Missing"), false},
		{"Undefined_Missing", Undefined[User]("Missing"), true},
		{"Undefined_Name", Undefined[User]("Name"), false},

		// Type
		{"Type_Name_String", Type[User]("Name", "string"), true},
		{"Type_Age_Number", Type[User]("Age", "number"), true},
		{"Type_Tags_Array", Type[User]("Tags", "array"), true},
		{"Type_Config_Object", Type[User]("Config", "object"), true},
		{"Type_Bio_String", Type[User]("Bio", "string"), true},

		// String operations
		{"Contains_Bio", Contains[User]("Bio", "Alice"), true},
		{"Contains_Bio_Fold", ContainsFold[User]("Bio", "alice"), true},
		{"Contains_Bio_False", Contains[User]("Bio", "Bob"), false},
		{"StartsWith_Bio", StartsWith[User]("Bio", "Hello"), true},
		{"StartsWith_Bio_Fold", StartsWithFold[User]("Bio", "hello"), true},
		{"EndsWith_Bio", EndsWith[User]("Bio", "Alice."), true},
		{"EndsWith_Bio_Fold", EndsWithFold[User]("Bio", "alice."), true},
		{"Matches_Bio", Matches[User]("Bio", ".*Alice.*"), true},
		{"Matches_Bio_Fold", MatchesFold[User]("Bio", ".*alice.*"), true},

		// In
		{"In_Age", In[User]("Age", 20, 30, 40), true},
		{"In_Age_False", In[User]("Age", 20, 40), false},
		{"In_Bio_Fold", InFold[User]("Bio", "HELLO, I AM ALICE."), true},

		// IgnoreCase on existing comparisons
		{"EqualFold", EqualFold[User]("Bio", "hello, i am alice."), true},
		{"NotEqualFold", NotEqualFold[User]("Bio", "hello, i am bob."), true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.cond.Evaluate(&u)
			if err != nil {
				t.Fatalf("Evaluate failed: %v", err)
			}
			if got != tt.want {
				t.Errorf("Evaluate() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestPredicatesSerialization(t *testing.T) {
	type Data struct {
		V string
	}

	tests := []struct {
		name string
		cond Condition[Data]
	}{
		{"Defined", Defined[Data]("V")},
		{"Undefined", Undefined[Data]("V")},
		{"Type", Type[Data]("V", "string")},
		{"Contains", Contains[Data]("V", "foo")},
		{"ContainsFold", ContainsFold[Data]("V", "foo")},
		{"In", In[Data]("V", "a", "b")},
		{"InFold", InFold[Data]("V", "A", "B")},
		{"Log", Log[Data]("test message")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, err := marshalCondition(tt.cond)
			if err != nil {
				t.Fatalf("marshalCondition failed: %v", err)
			}
			// Simulate roundtrip
			data, _ := json.Marshal(s)
			cond2, err := unmarshalCondition[Data](data)
			if err != nil {
				t.Fatalf("unmarshalCondition failed: %v", err)
			}

			d := Data{V: "foo"}
			ok1, _ := tt.cond.Evaluate(&d)
			ok2, _ := cond2.Evaluate(&d)
			if ok1 != ok2 {
				t.Errorf("Evaluate mismatch: %v != %v", ok1, ok2)
			}
		})
	}
}
