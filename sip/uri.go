package sip

import (
	"context"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/internal/util"
	"github.com/ghettovoice/gosip/uri"
)

// URI represents a SIP or SIPS URI.
// See [sip.URI].
type URI = uri.SIP

func ParseURI[T ~string | ~[]byte](s T) (*URI, error) { return errors.Wrap2(uri.ParseSIP(s)) }

// UserInfo represents user info for a [URI].
// See [sip.UserInfo].
type UserInfo = uri.UserInfo

func UserWithName(usrname string) UserInfo { return uri.User(usrname) }

func UserWithNamePassword(usrname, passwd string) UserInfo { return uri.UserPassword(usrname, passwd) }

// AnyURI represents generic AnyURI (SIP, SIPS, Tel, ...etc).
// See [uri.AnyURI].
type AnyURI = uri.URI

// ParseAnyURI parses any URI from a given input s (string or []byte).
// See [uri.Parse].
func ParseAnyURI[T ~string | ~[]byte](s T) (AnyURI, error) { return errors.Wrap2(uri.Parse(s)) }

// URIConverter converts any URI to a SIP URI.
type URIConverter interface {
	ConvertURI(ctx context.Context, anyURI AnyURI) (*URI, error)
}

// StdURIConverter is the default URI converter.
type StdURIConverter struct{}

var defURIConverter = &StdURIConverter{}

func DefaultURIConverter() *StdURIConverter { return defURIConverter }

// ConvertURI converts any URI to a SIP URI.
// It supports SIP, SIPS, and Tel URIs.
// Converting from Tel URI doesn't use any external services, it just uses [uri.Tel.ToSIP] method.
func (*StdURIConverter) ConvertURI(_ context.Context, anyURI AnyURI) (*URI, error) {
	switch util.LCase(anyURI.Scheme()) {
	case "sip":
		fallthrough
	case "sips":
		u, ok := anyURI.(*URI)
		if !ok {
			return nil, errors.ErrorfWrap("unsupported URI type %q", anyURI)
		}
		return u, nil
	case "tel":
		u, ok := anyURI.(*uri.Tel)
		if !ok {
			return nil, errors.ErrorfWrap("unsupported URI type %q", anyURI)
		}
		return u.ToSIP(), nil
	default:
		return nil, errors.ErrorfWrap("unsupported URI scheme %q", anyURI.Scheme())
	}
}

func ConvertURI(ctx context.Context, anyURI AnyURI) (*URI, error) {
	return errors.Wrap2(defURIConverter.ConvertURI(ctx, anyURI))
}

func cleanReqURI(u *URI) *URI {
	u2 := u.Clone().(*URI) //nolint:forcetypeassert
	u2.Params.Delete("method")
	u2.Headers.Clear()
	return u2
}
