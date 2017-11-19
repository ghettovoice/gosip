package base

type MaybeString interface {
	String() string
}

type String struct {
	Str string
}

func (str String) String() string {
	return str.Str
}
