# gosip

[![Go Reference](https://pkg.go.dev/badge/github.com/ghettovoice/gosip.svg)](https://pkg.go.dev/github.com/ghettovoice/gosip)
[![Tests](https://github.com/ghettovoice/gosip/actions/workflows/test.yml/badge.svg)](https://github.com/ghettovoice/gosip/actions/workflows/test.yml)
[![Coverage Status](https://coveralls.io/repos/github/ghettovoice/gosip/badge.svg?branch=master)](https://coveralls.io/github/ghettovoice/gosip?branch=master)
[![CodeQL](https://github.com/ghettovoice/gosip/actions/workflows/github-code-scanning/codeql/badge.svg)](https://github.com/ghettovoice/gosip/actions/workflows/github-code-scanning/codeql)

`gosip` provides building blocks for SIP applications in Go, based on
[RFC 3261](https://datatracker.ietf.org/doc/html/rfc3261).

The project is under active development and the public API may change between
releases.

## Current features

- SIP message parsing and rendering, typed headers, and SIP, SIPS, and TEL
  URIs.
- Pluggable packet and stream transports with connection pooling and
  [RFC 3263](https://datatracker.ietf.org/doc/html/rfc3263) DNS resolution.
- Client and server INVITE and non-INVITE transactions, including timers and
  retransmissions.
- Route-based stateful and stateless proxying.
- Optional Digest and Bearer authentication helpers.
- Attachable components and transport, transaction, and proxy statistics.

Higher-level user agent, dialog, and registrar layers are not implemented yet.

## Installation

```sh
go get github.com/ghettovoice/gosip
```

## Documentation

See the [documentation index](./docs/README.md) for detailed guides.

## License

See [LICENSE](./LICENSE) file for a full text.
