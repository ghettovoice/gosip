package sip

import (
	"slices"

	"github.com/ghettovoice/gosip/internal/util"
	"github.com/ghettovoice/gosip/sip/header"
)

func (elm *Element) Capabilities() ElementCapabilities {
	return elm.curCpb.Load().(ElementCapabilities) //nolint:forcetypeassert
}

// ElementCapabilities represents the capabilities of an element.
type ElementCapabilities struct {
	Methods   []RequestMethod
	Options   []header.OptionTag
	MIMETypes []header.MIMERange
	// Languages []header.LanguageRange
	// Encodings []header.EncodingRange
}

func (cpb ElementCapabilities) SupportsOption(opt header.OptionTag) bool {
	return slices.ContainsFunc(cpb.Options, func(o header.OptionTag) bool { return o.Equal(opt) })
}

func (cpb ElementCapabilities) SupportsMethod(method RequestMethod) bool {
	return slices.ContainsFunc(cpb.Methods, func(m RequestMethod) bool { return m.Equal(method) })
}

func (cpb ElementCapabilities) SupportsMIMEType(mime header.MIMERange) bool {
	if len(cpb.MIMETypes) == 0 {
		return util.EqFold(mime.Type, "application") && util.EqFold(mime.Subtype, "sdp")
	}

	return slices.ContainsFunc(cpb.MIMETypes, func(m header.MIMERange) bool {
		return m.Type == "*" || mime.Type == "*" ||
			util.EqFold(m.Type, mime.Type) &&
				(m.Subtype == "*" || mime.Subtype == "*" || util.EqFold(m.Subtype, mime.Subtype))
	})
}

// func (cpb ElementCapabilities) SupportsLanguage(lang header.LanguageRange) bool {
// 	if len(cpb.Languages) == 0 {
// 		return true
// 	}

// 	return slices.ContainsFunc(cpb.Languages, func(l header.LanguageRange) bool {
// 		return l.Lang == "*" || lang.Lang == "*" || l.Lang.Equal(lang.Lang)
// 	})
// }

// func (cpb ElementCapabilities) SupportsEncoding(enc header.EncodingRange) bool {
// 	if len(cpb.Encodings) == 0 {
// 		return enc.Encoding.Equal(header.Encoding("identity"))
// 	}

// 	return slices.ContainsFunc(cpb.Encodings, func(e header.EncodingRange) bool {
// 		return e.Encoding == "*" || enc.Encoding == "*" || e.Encoding.Equal(enc.Encoding)
// 	})
// }

func (cpb ElementCapabilities) NewAllowHeader() header.Allow {
	if len(cpb.Methods) == 0 {
		return nil
	}
	return slices.Clone(cpb.Methods)
}

func (cpb ElementCapabilities) NewSupportedHeader() header.Supported {
	if len(cpb.Options) == 0 {
		return nil
	}
	return slices.Clone(cpb.Options)
}

func (cpb ElementCapabilities) NewAcceptHeader() header.Accept {
	if len(cpb.MIMETypes) == 0 {
		return nil
	}
	return util.CloneSliceFunc(cpb.MIMETypes, func(m header.MIMERange) header.MIMERange { return m.Clone() })
}

// func (cpb ElementCapabilities) NewAcceptLanguageHeader() header.AcceptLanguage {
// 	if len(cpb.Languages) == 0 {
// 		return nil
// 	}
// 	return util.CloneSliceFunc(cpb.Languages, func(l header.LanguageRange) header.LanguageRange { return l.Clone() })
// }

// func (cpb ElementCapabilities) NewAcceptEncodingHeader() header.AcceptEncoding {
// 	if len(cpb.Encodings) == 0 {
// 		return nil
// 	}
// 	return util.CloneSliceFunc(cpb.Encodings, func(e header.EncodingRange) header.EncodingRange { return e.Clone() })
// }
