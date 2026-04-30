# Quality Guidelines

> Code quality standards for backend development.

---

## Overview

<!--
Document your project's quality standards here.

Questions to answer:
- What patterns are forbidden?
- What linting rules do you enforce?
- What are your testing requirements?
- What code review standards apply?
-->

(To be filled by the team)

---

## Forbidden Patterns

<!-- Patterns that should never be used and why -->

(To be filled by the team)

---

## Required Patterns

<!-- Patterns that must always be used -->

### Scenario: Client Service Address UX Contract

#### 1. Scope / Trigger

- Trigger: CLI, installer, Web, or docs changes that show users how to connect a NetsGo client.
- Applies to: `netsgo client --server`, `netsgo install` client prompts, Web "Add Client" connection command, README quick-start examples, and tests for those surfaces.

#### 2. Signatures

- CLI flag: `netsgo client --server <service-address>`
- Environment key: `NETSGO_SERVER=<service-address>`
- Go normalization API: `clientaddr.Normalize(raw string, mode clientaddr.Mode) (clientaddr.Address, error)`
- Web command resolver: produce `netsgo client --server <service-address> --key <raw-key>` from the effective server address when available.

#### 3. Contracts

- User-facing primary value is a service address: `http://host[:port]` or `https://host[:port]`.
- `ws://` and `wss://` remain accepted for compatibility, but are normalized to `http://` and `https://` base service addresses before persistence or display in primary command examples.
- Control/data WebSocket endpoints are derived internals:
  - `http://host` -> `ws://host/ws/control` and `ws://host/ws/data`
  - `https://host` -> `wss://host/ws/control` and `wss://host/ws/data`
- Web Add Client must prefer the effective configured service address over a stale persisted value when both are available.

#### 4. Validation & Error Matrix

- Empty value -> `service address cannot be empty`.
- Whitespace in value -> `service address cannot contain whitespace`.
- Managed install without a scheme -> reject; ask for `http://`, `https://`, `ws://`, or `wss://`.
- Unsupported scheme -> reject.
- User info, non-root path, query, or fragment -> reject.
- Invalid port -> reject.
- Legacy `ws(s)` input -> accept and normalize, but do not present as the first-use recommended form.

#### 5. Good/Base/Bad Cases

- Good: `netsgo client --server https://netsgo.example.com --key sk-...`
- Base: `netsgo client --server http://netsgo.zsio.dev:9527 --key sk-...`
- Bad: telling a first-time user to copy `wss://netsgo.example.com/ws/control` or manually convert `https` to `wss`.

#### 6. Tests Required

- Go tests for `internal/clientaddr` normalization and error wording.
- Go tests for install prompt summaries using `Service address` and not exposing control/data endpoints as primary action rows.
- CLI help tests that prefer `http(s)` examples and only mention `ws(s)` as compatibility.
- Frontend tests for Web Add Client command generation, including `effective_server_addr` precedence and invalid legacy address fallback.

#### 7. Wrong vs Correct

##### Wrong

```text
Client install address: wss://netsgo.example.com
Run: netsgo client --server wss://netsgo.example.com --key sk-...
```

##### Correct

```text
Client install address: https://netsgo.example.com
Run: netsgo client --server https://netsgo.example.com --key sk-...
```

---

## Testing Requirements

<!-- What level of testing is expected -->

(To be filled by the team)

---

## Code Review Checklist

<!-- What reviewers should check -->

(To be filled by the team)
