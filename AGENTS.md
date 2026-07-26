# Repository Guidelines

## Project Structure & Module Organization

NetsGo is a Go backend plus React frontend monorepo that builds into the `netsgo` CLI. Command entrypoints live in `cmd/netsgo/`; server, client, storage, install, and TUI code lives in `internal/`; shared protocol and transport packages live in `pkg/`. The web admin app is under `web/`, with business UI in `web/src/components/custom/`, shadcn UI in `web/src/components/ui/`, routes in `web/src/routes/`, hooks in `web/src/hooks/`, and API helpers in `web/src/lib/`. Go tests sit beside packages as `*_test.go`; system E2E tests live in `test/e2e/`; Playwright tests live in `web/e2e/`.

## Source of Truth

When documentation and implementation disagree, trust sources in this order: code, tests, `Makefile`, `.github/workflows/*.yml`, `README.md` or `web/README.md`, then historical notes under `docs/`. Inspect these before changing behavior; do not infer APIs, state machines, deployment topology, or storage format from names alone.

## Architecture Invariants

`netsgo` is one binary with `server`, `client`, `benchmark`, and `docs` subcommands. The server should be treated as a single-instance control plane backed by local SQLite, not as a distributed system. The web frontend and server ship in the same binary and version; web assets are embedded from `web/dist/`. Client/server compatibility matters across versions, but web/server fallback paths normally do not. Shared wire contracts belong in `pkg/protocol/`. The single server listener carries the web panel, REST API, SSE, control WebSocket, and data WebSocket paths.

## Build, Test, and Development Commands

- `make build`: builds web assets, then compiles `bin/netsgo`.
- `make build-go`: compiles the Go binary; requires `web/dist/` for embedded assets.
- `DEV_INIT_ADMIN_PASSWORD=... make dev-server`: runs the server with `-tags dev`.
- `DEV_KEY=... make dev-client`: runs a local client against the dev server.
- `make dev-web`: starts the Vite frontend server.
- `make test`: runs `go test ./...`.
- `cd web && bun run lint && bun run build`: validates frontend linting and TypeScript/Vite build.
- `make test-system-e2e-nginx` or `make test-system-e2e-caddy`: runs proxy-backed E2E coverage.

## Where to Look First

- CLI flags and subcommands: `cmd/netsgo/`.
- Server API, auth, sessions, SSE, and tunnel lifecycle: `internal/server/`.
- Client connection, reconnect, probes, and tunnel execution: `internal/client/`.
- Shared protocol messages and payloads: `pkg/protocol/`.
- Web API calls and routing: `web/src/lib/api.ts`, `web/src/lib/router.ts`, and related hooks.
- Web business UI: `web/src/components/custom/`.
- SQLite migrations: `internal/server/migrations/` plus `internal/server/storage_schema.go`.

## Coding Style & Naming Conventions

Use `gofmt` defaults for Go; keep package names lowercase and tests named `TestXxx`. Keep protocol types in `pkg/protocol/` rather than duplicating wire structs. Frontend code uses TypeScript, React, TanStack Router/Query, ESLint, and Bun. Put feature UI in `web/src/components/custom/`; avoid hand-editing `web/src/components/ui/`. Follow existing names such as `use-*.test.ts`, `*Dialog.tsx`, and `*Table.tsx`.

## Agent-Specific Rules

Make the smallest correct change and verify it. Do not hand-edit `web/src/components/ui/` unless a shadcn source change is explicitly required. Do not scatter raw `fetch` calls outside the API layer. Do not create protocol structs parallel to `pkg/protocol/`. Do not weaken auth, session, online-state, legacy provision, storage projection, or client/server compatibility code without a versioned migration and rollback plan. Do not add target-service health checks by default; NetsGo may track link/runtime health, but probing user services requires explicit design and opt-in configuration.

## Testing Guidelines

Prefer the smallest test that covers the changed behavior: package Go tests for backend logic, frontend unit tests near the affected module, and Playwright/system E2E for cross-process flows. For server/client/protocol changes, run relevant package tests first, then `make test` when feasible. For frontend changes, run at least `bun run build`; add `bun run lint` for style-sensitive work.

## Verification Matrix

- Go-only local logic: run the affected package tests.
- Server/client/protocol/auth/session/channel changes: run relevant package tests, then `make test` when feasible.
- Frontend TypeScript or UI changes: run `cd web && bun run build`; add `bun run lint` for lint-sensitive edits.
- Embedded assets, release output, or build pipeline changes: run `make build`.
- Data channel, reverse proxy, TLS, reconnect, or recovery changes: prefer nginx/caddy E2E targets under `test/e2e/`.

## Commit & Pull Request Guidelines

Recent history uses short imperative subjects, often Conventional Commit prefixes such as `fix:` and `feat:`. Keep commits focused, for example `fix: align tunnel endpoint inputs`. Pull requests should describe behavior changes, list verification commands, link issues when available, and include screenshots for visible UI changes. Note skipped checks and why.

## Release & Compatibility Notes

Releases are tag-driven. Stable tags use `vMAJOR.MINOR.PATCH`; beta tags use `vMAJOR.MINOR.PATCH-beta.N`. A push to `main` is not a release. Preserve client/server upgrade behavior unless a deprecation window, release note, migration, rollback path, and compatibility/E2E coverage are part of the change.

## Security & Configuration Tips

Do not commit secrets, API keys, generated local state, or predictable admin passwords. Development server startup requires `DEV_INIT_ADMIN_PASSWORD`; client startup requires `DEV_KEY`. Local runtime data may be written under `~/.netsgo/`, so isolate or clean that directory when reproducing stateful bugs.
