# gosip

[![Go Reference](https://pkg.go.dev/badge/github.com/ghettovoice/gosip.svg)](https://pkg.go.dev/github.com/ghettovoice/gosip)
[![Go Report Card](https://goreportcard.com/badge/github.com/ghettovoice/gosip)](https://goreportcard.com/report/github.com/ghettovoice/gosip)
[![Tests](https://github.com/ghettovoice/gosip/actions/workflows/test.yml/badge.svg)](https://github.com/ghettovoice/gosip/actions/workflows/test.yml)
[![Coverage Status](https://coveralls.io/repos/github/ghettovoice/gosip/badge.svg?branch=master)](https://coveralls.io/github/ghettovoice/gosip?branch=master)
[![CodeQL](https://github.com/ghettovoice/gosip/actions/workflows/github-code-scanning/codeql/badge.svg)](https://github.com/ghettovoice/gosip/actions/workflows/github-code-scanning/codeql)

Package `gosip` provides SIP stack as described in [RFC 3261](https://datatracker.ietf.org/doc/html/rfc3261).

RFCs:

- [RFC 3261](https://datatracker.ietf.org/doc/html/rfc3261)
- [RFC 3263](https://datatracker.ietf.org/doc/html/rfc3263)
- [RFC 3581](https://datatracker.ietf.org/doc/html/rfc3581)
- [RFC 3966](https://datatracker.ietf.org/doc/html/rfc3966)
- [RFC 5954](https://datatracker.ietf.org/doc/html/rfc5954)
- [RFC 8898](https://datatracker.ietf.org/doc/html/rfc8898)

## Installation

TODO...

## Usage

TODO...

## Features

### Transaction Persistence

The library supports **transaction snapshots** for persistence and recovery after server restarts:

```go
// Take a snapshot
snapshot := tx.Snapshot()
data, _ := json.Marshal(snapshot)
db.Save(tx.Key(), data)

// Restore from snapshot
var snapshot sip.ServerTransactionSnapshot
json.Unmarshal(data, &snapshot)
opts := &sip.ServerTransactionOptions{/* tx options */}
tx, _ := sip.RestoreInviteServerTransaction(&snapshot, transport, opts)
```

See [TRANSACTION_PERSISTENCE.md](./doc/TRANSACTION_PERSISTENCE.md) for detailed documentation and examples.

## License

See [LICENSE](./LICENSE) file for a full text.
