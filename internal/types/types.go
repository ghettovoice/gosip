// Package types contains common types used across the gosip project.
package types

import (
	"fmt"
	"io"

	"github.com/google/go-cmp/cmp"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/internal/util"
)

type ContextKey string

// Renderer is an interface that is used to render a type to a string or a writer.
type Renderer interface {
	// RenderTo renders the type to a writer with the given options.
	RenderTo(w io.Writer, opts ...RenderOptions) (int, error)
}

// RenderOptions is a struct that is used to pass options to rendering methods.
type RenderOptions struct {
	// Compact is a boolean flag that is used to render a type in compact form.
	Compact bool `json:"compact"`
}

func Render(v any, opts ...RenderOptions) string {
	sb := util.GetStringBuilder()
	defer util.FreeStringBuilder(sb)

	if r, ok := v.(Renderer); ok {
		r.RenderTo(sb, opts...)
	} else {
		fmt.Fprintf(sb, "%v", v)
	}
	return sb.String()
}

type ValidFlag interface {
	IsValid() bool
}

// IsValid returns true if the value has method `IsValid() bool` and it returns true.
func IsValid(v any) bool {
	vv, ok := v.(ValidFlag)
	return ok && vv.IsValid()
}

type Validatable interface {
	Validate() error
}

// Validate validates the value if it has method `Validate() error`,
// otherwise returns an [errorutil.ErrInvalidArgument] error.
func Validate(v any) error {
	if v == nil {
		return errors.ErrorWrap("nil value")
	}

	vv, ok := v.(Validatable)
	if !ok {
		return errors.ErrorWrap("value doesn't implement types.Validatable")
	}

	return errors.Wrap(vv.Validate())
}

type Equalable interface {
	Equal(val any) bool
}

// IsEqual returns true if the values are equal.
func IsEqual(v1, v2 any) bool {
	return cmp.Equal(v1, v2)
}

type Cloneable[T any] interface {
	Clone() T
}

// Clone clones the value if it has method `Clone() T`, otherwise returns a zero value.
func Clone[T any](v any) T {
	if v1, ok := v.(Cloneable[T]); ok {
		return v1.Clone()
	}

	if v == nil {
		var zero T
		return zero
	}

	v1, _ := v.(T)
	return v1
}
