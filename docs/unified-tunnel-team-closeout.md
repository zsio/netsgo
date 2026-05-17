# Unified Tunnel Team Closeout Notes

Date: 2026-05-17

## What this branch is doing

This branch preserves the current closeout state of the unified tunnel work that
was continued through the OMX team run `continue-netsgo-unifi-5d6ed661`.

The immediate goal was not to finish every future peer-direct capability. The
closeout goal was narrower:

1. reconcile the completed two-worker team run;
2. keep the server-relay tunnel path working after the DataStreamHeader stream
   contract changes;
3. preserve stable tunnel identity across client-created tunnels, storage, and
   traffic accounting;
4. leave direct-transport limitations explicit instead of silently claiming full
   peer-direct support.

## Team outcome

The team run reached terminal completion before shutdown:

- total tasks: 4
- completed: 4
- failed: 0
- pending: 0
- in progress: 0
- workers: 2

After terminal status was confirmed, the team runtime was shut down. The final
`omx team status continue-netsgo-unifi-5d6ed661` result became `status: missing`,
which is the expected state after shutdown cleanup.

## Code changes captured here

### Stable identity for client-created tunnels

Client-initiated tunnel creation previously registered a local
`ProxyNewRequest` keyed by name before the server had assigned the stable tunnel
ID. After the server opened a server-relay data stream using the stable
`DataStreamHeader.TunnelID`, the client could fail lookup with an "unknown
tunnel id" error.

The closeout fix makes the server include server-owned tunnel metadata in
`ProxyCreateResponse`:

- `id`
- `transport_policy`
- `actual_transport`
- `provision_revision`

On successful create response, the client updates its local proxy config with
that metadata so later DataStreamHeader routing can resolve by stable tunnel ID.

### DataStreamHeader test alignment

The TCP proxy accept-loop test was still acting like the mock client should
consume the old name-based stream header. The server now writes the versioned
`DataStreamHeader`, so the test mock was updated to decode and discard that
header before relaying payload bytes to the local backend.

### Worker-integrated storage/traffic identity work

The team shutdown merged the worker updates that preserve tunnel identity in UDP
traffic accounting. In particular, UDP reverse traffic now records traffic using
the full tunnel config rather than only the tunnel name/type tuple.

## Boundaries and known gaps

This branch does **not** implement full peer-direct TCP/UDP data transport.
Current behavior remains intentionally server-relay centered.

Important boundary:

- `direct_only` must not be treated as healthy server-relay support.
- Existing server/client checks reject server-relay DataStreamHeader usage for
  `direct_only` paths where that path is reached.
- A complete peer-direct hard gate for all direct TCP/UDP flows is still future
  work and should be designed separately.

Also out of scope for this closeout:

- target service health checks;
- active probing of user backend services;
- support for `unix_socket`, `static_file`, or `serial_device` endpoint types.

## Verification evidence

After team shutdown and worker merge reconciliation, the following checks passed:

```bash
git diff --check
go test -tags dev ./...
go test ./internal/server ./internal/client ./pkg/protocol -count=1
cd web && bun run lint && bun run build
```

The Vite build still emits the existing large chunk warning; it did not fail the
build.

## Follow-up recommendations

1. Add a focused design/test plan before implementing peer-direct TCP/UDP.
2. Keep DataStreamHeader as the stream-routing contract; avoid adding another
   parallel stream header shape.
3. Preserve stable tunnel IDs in all storage, event, and traffic paths.
4. If target service health checks are ever added, make them explicit,
   user-configured probes and keep them separate from NetsGo link/runtime
   health.
