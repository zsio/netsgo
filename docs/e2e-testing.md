# E2E Testing

NetsGo has two E2E layers:

- **System E2E**: Go tests drive real Docker Compose topologies. This is the source of truth for server, client, tunnel runtime, reverse proxy, and data-path behavior.
- **Playwright UI E2E**: browser tests verify user workflows. They run headed by default and must not be used as the primary proof of data-plane correctness.

## System E2E

System E2E lives in `test/e2e/system_e2e_test.go`.

Compose files define infrastructure only:

- `test/e2e/docker-compose.system.yml`
- `test/e2e/docker-compose.proxy.nginx.yml`
- `test/e2e/docker-compose.proxy.caddy.yml`

The Go test owns all business actions:

- admin login
- API key creation
- client readiness checks
- `/api/tunnels` mutation calls
- tunnel runtime polling
- HTTP and SOCKS5 traffic assertions
- restart and recovery assertions

Shell scripts must not create tunnels or encode scenario logic. Compose services may use scripts only for process bootstrapping, such as waiting for a client key and starting `netsgo client`.

Run the full system E2E matrix:

```bash
make test-system-e2e
```

Run one proxy variant:

```bash
make test-system-e2e-nginx
make test-system-e2e-caddy
```

Useful local stack commands:

```bash
NETSGO_ADMIN_PASS="$(openssl rand -base64 18)" make system-e2e-up
make system-e2e-logs
make system-e2e-down
make system-e2e-clean
```

## Topology Rules

System E2E must model real network boundaries:

- `server` and clients share the `control` network.
- target backends live on a target-side backend network.
- only `target-client` can reach target backends directly.
- `ingress-client` exposes client-side ingress ports to the host only when the scenario needs an external caller.
- `proxy` is an overlay service, not a business-test controller.

This keeps data-path assertions meaningful. For example, a client-to-client SOCKS5 test must prove that the ingress client accepts the SOCKS5 connection while the target client performs the backend dial.

## Current System Scenarios

`TestSystemE2E` covers:

- admin login rejection and admin API authorization.
- HTTP `server_expose` through nginx/caddy reverse proxy.
- HTTP `server_expose` through direct server upstream.
- HTTP Basic authentication on `server_expose` routes.
- SOCKS5 `server_expose` CONNECT through server ingress.
- SOCKS5 `client_to_client` CONNECT through ingress-client and target-client.
- SOCKS5 username/password authentication, wrong-password rejection, and no-auth method rejection.
- SOCKS5 ingress source CIDR rejection.
- SOCKS5 target policy denial.
- TCP `client_to_client` data path.
- UDP `client_to_client` datagram echo path.
- multiple simultaneous TCP tunnels to distinct target backends, with isolation assertions.
- concurrent data streams through one TCP tunnel and one SOCKS5 tunnel.
- bounded latency sanity checks for fast TCP data-path requests.
- fast-tunnel responsiveness while another tunnel holds slow backend connections open.
- proxy restart recovery for HTTP.
- ingress-client restart recovery for TCP and UDP c2c.
- target-client restart recovery for SOCKS5 c2c.
- server restart recovery with persisted tunnel restoration and data-path revalidation.

Add new runtime scenarios here when the behavior depends on real process, network, reverse proxy, restart, or Docker topology.

## Playwright UI E2E

Playwright tests live under `web/e2e`.

They run against `test/e2e/docker-compose.playwright.yml` and focus on UI behavior:

- login
- form behavior
- create/edit/stop/resume/delete workflows
- field validation and visible feedback

Playwright is configured with `headless: false` in `web/e2e/playwright.config.ts`.

For local visual runs on macOS, start Chrome with CDP enabled first:

```bash
devtools
```

Then run the smoke suite through that visible Chrome:

```bash
make test-playwright-e2e-cdp-smoke
```

The CDP target defaults to `http://127.0.0.1:9222` and can be overridden:

```bash
LOCAL_CHROME_CDP_ENDPOINT=http://127.0.0.1:9223 make test-playwright-e2e-cdp-smoke
```

The local CDP target opens a visible tab in the Chrome profile launched by `devtools`.
It also uses a small default delay between browser actions and keeps the final tab open so the run is easy to inspect.
Override these when needed:

```bash
LOCAL_CHROME_CDP_SLOW_MO_MS=500 LOCAL_CHROME_CDP_FINISH_DELAY_MS=15000 LOCAL_CHROME_CDP_KEEP_TAB=0 make test-playwright-e2e-cdp-smoke
```

CI does not use the local CDP profile. It launches Playwright's Chromium under a virtual Linux display via `xvfb-run -a`.

Run the smoke suite:

```bash
make test-playwright-e2e-smoke
```

The smoke suite is intentionally short. It creates TCP and UDP `client_to_client` tunnels from the UI, then verifies host-to-ingress traffic reaches the Docker backend services.
Use the full suite for UI lifecycle, validation, and conflict scenarios:

Run all Playwright E2E:

```bash
make test-playwright-e2e-full
```

## Design Rules

- Do not add new system scenarios to shell scripts.
- Do not create tunnels through legacy APIs in system E2E.
- Prefer `/api/tunnels` for all system E2E tunnel mutations.
- Keep Compose files declarative: services, ports, networks, volumes.
- Keep assertions in Go or Playwright tests, not in container startup scripts.
- Browser E2E must stay headed unless a specific CI environment requires a virtual display.
