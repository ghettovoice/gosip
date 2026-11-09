# Project Map

- gosip is a Go SIP stack (RFC 3261); the root package holds module version metadata, not the main stack API.
- `sip/`: messages, packet/stream parsing, transport and transaction interfaces, elements, proxy behavior and routing. Concrete transport implementations live in `sip/transport/`; SIP header types/parsers in `sip/header/`.
- `uri/`: SIP/SIPS and telephone URI handling. `internal/shared/` supplies shared address, value, rendering and protocol types (some are exposed through public aliases).
- `internal/grammar/`: parsing/escaping and RFC 3261/3966 grammar resources. `pkg/dns/`, `pkg/errclass/`, `pkg/log/`: supporting public packages.
- `internal/testutil/`: reusable test support, including `dnstest` and `siptest`. `docs/proxy.md`: proxy documentation.
- `AGENTS.md` points to authoritative `.agents/rules/`; project-specific rules override conflicting generic Go examples. Preserve SIP wire formats, names and RFC terminology.
- Language/toolchain and build dependencies: `mem:tech_stack`.
- Repository-specific error, test and formatting contracts: `mem:conventions`.
- Task commands, package selection and freshness caveats: `mem:suggested_commands`.
- Required verification before reporting coding work complete: `mem:task_completion`.
- Rules for maintaining these notes and their reference graph: `mem:memory_maintenance`.
