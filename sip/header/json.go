package header

import "github.com/ghettovoice/gosip/internal/errors"

// unmarshalHdrJSON accepts parsed value and pointer forms, and assigns only after successful decoding.
func unmarshalHdrJSON[T any](data []byte, dst *T, name Name, emptyValue func(string) bool) error {
	parsed, err := FromJSON(data)
	if err != nil {
		return errors.Wrap(err)
	}

	var result T
	switch hdr := any(parsed).(type) {
	case nil:
	case *T:
		if hdr == nil {
			return errors.Wrap(newUnexpectHdrTypeErr(parsed))
		}
		result = *hdr
	case T:
		result = hdr
	default:
		anyHdr, ok := parsed.(*Any)
		if !ok || anyHdr == nil || emptyValue == nil ||
			!anyHdr.CanonicName().Equal(name) || !emptyValue(anyHdr.Value) {
			return errors.Wrap(newUnexpectHdrTypeErr(parsed))
		}
	}

	*dst = result
	return nil
}

func emptyHdrValue(value string) bool {
	return value == ""
}

func emptyNameAddrValue(value string) bool {
	return value == "" || value == "<>"
}
