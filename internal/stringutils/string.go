package stringutils

import (
	"cmp"
	"fmt"
	"io"
	"strings"
)

func UCase[T ~string](s T) T { return T(strings.ToUpper(string(s))) }

func LCase[T ~string](s T) T { return T(strings.ToLower(string(s))) }

func TrimSP[T ~string](s T) T { return T(strings.TrimSpace(string(s))) }

func RenderTo(w io.Writer, v ...any) error {
	for _, v := range v {
		switch v := v.(type) {
		case interface{ RenderTo(w io.Writer) error }:
			if err := v.RenderTo(w); err != nil {
				return err
			}
		default:
			if _, err := fmt.Fprint(w, v); err != nil {
				return err
			}
		}
	}
	return nil
}

func CmpKVs[T ~string](kv1, kv2 []T) int { return cmp.Compare(kv1[0], kv2[0]) }
