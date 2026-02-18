package deep

import (
	"testing"
)

func TestEqual_Basic(t *testing.T) {
	if !Equal(1, 1) {
		t.Error("1 != 1")
	}
	if Equal(1, 2) {
		t.Error("1 == 2")
	}
}

func TestEqual_Options(t *testing.T) {
	type S struct {
		A int
		B string
	}
	s1 := S{A: 1, B: "foo"}
	s2 := S{A: 1, B: "bar"}

	if Equal(s1, s2) {
		t.Error("s1 == s2 without ignore")
	}

	if !Equal(s1, s2, IgnorePath("B")) {
		t.Error("s1 != s2 with ignore B")
	}
	
	// Test pointer
	if !Equal(&s1, &s2, IgnorePath("B")) {
		t.Error("&s1 != &s2 with ignore B")
	}
}

func TestEqual_Map(t *testing.T) {
	m1 := map[string]int{"a": 1, "b": 2}
	m2 := map[string]int{"a": 1, "b": 3}

	if Equal(m1, m2) {
		t.Error("m1 == m2 without ignore")
	}

	if !Equal(m1, m2, IgnorePath("/b")) {
		t.Error("m1 != m2 with ignore /b")
	}
}

func TestEqual_Slice(t *testing.T) {
	sl1 := []int{1, 2, 3}
	sl2 := []int{1, 4, 3}

	if Equal(sl1, sl2) {
		t.Error("sl1 == sl2 without ignore")
	}

	if !Equal(sl1, sl2, IgnorePath("/1")) {
		t.Error("sl1 != sl2 with ignore /1")
	}
}
