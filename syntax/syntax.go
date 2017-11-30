package syntax

type Error interface {
	error
	// Syntax indicates that this is syntax error
	Syntax() bool
}

type InvalidStartLineError string

func (err InvalidStartLineError) Syntax() bool    { return true }
func (err InvalidStartLineError) Malformed() bool { return false }
func (err InvalidStartLineError) Broken() bool    { return true }
func (err InvalidStartLineError) Error() string   { return "InvalidStartLineError: " + string(err) }

type ParserWriteError string

func (err ParserWriteError) Syntax() bool  { return false }
func (err ParserWriteError) Error() string { return "ParserWriteError: " + string(err) }
