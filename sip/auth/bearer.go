package auth

import (
	"net/url"
	"strings"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/internal/shared"
	"github.com/ghettovoice/gosip/uri"
)

// validateAuthzServer renders the configured authorization-server URI,
// requires an absolute HTTPS URL with a host and no userinfo or fragment,
// and returns the original rendered text for byte-identical trust
// comparisons plus an independent copy for retention. Error text never
// embeds URL contents: the URI may carry credentials or tokens. No
// normalization, aliasing, or network access happens here.
func validateAuthzServer(as uri.URI) (rendered string, cloned uri.URI, err error) {
	if as == nil {
		return "", nil, errors.ErrorWrap("missing authorization server URI")
	}
	rendered, err = renderURI(as)
	if err != nil {
		return "", nil, errors.Wrap(err)
	}
	u, err := url.Parse(rendered)
	if err != nil {
		return "", nil, errors.ErrorWrap("authorization server URI is not parseable")
	}
	switch {
	case u.Scheme != "https":
		return "", nil, errors.ErrorWrap("authorization server URI is not HTTPS")
	case u.Host == "" || u.Hostname() == "":
		return "", nil, errors.ErrorWrap("authorization server URI has no host")
	case u.User != nil:
		return "", nil, errors.ErrorWrap("authorization server URI must not carry userinfo")
	case strings.Contains(rendered, "#"):
		return "", nil, errors.ErrorWrap("authorization server URI must not carry a fragment")
	}
	return rendered, shared.Clone[uri.URI](as), nil
}

// isBearerToken reports whether token is a syntactically acceptable Bearer
// credential value: non-empty visible ASCII without whitespace, control
// bytes, or non-ASCII bytes. It does not interpret token structure.
func isBearerToken(token string) bool {
	if token == "" {
		return false
	}
	for i := 0; i < len(token); i++ {
		if c := token[i]; c <= 0x20 || c >= 0x7f {
			return false
		}
	}
	return true
}
