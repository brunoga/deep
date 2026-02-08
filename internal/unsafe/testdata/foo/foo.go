package foo

type S struct {
	A int // exported
	b int // unexported
}

func NewS(a, b int) S {
	return S{a, b}
}

func (s S) GetB() int {
	return s.b
}
