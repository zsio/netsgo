# Version Update Notice

This document records the web update notice design for NetsGo.

NetsGo is still pre-release, so the implementation should prefer simple and correct behavior over compatibility with older UI or API shapes.

## Goals

- Show an update notice near the version text when a newer compatible NetsGo version exists.
- Keep version lookup throttled so dashboard navigation does not repeatedly query upstream release pages.
- Reuse the same version comparison rules for server and client versions.
- Present different update actions for direct binary, Docker, and managed service deployments.

## Version Check Flow

The web UI calls:

```text
GET /api/version/check?version=<current-version>
```

The server uses `pkg/updater.CheckForUpdate` to find the latest compatible release.

Current channel rules:

- Stable versions only compare against stable releases.
- Beta versions compare against beta releases.
- Development or dirty builds may compare against the latest compatible published version.

The server checks CNB first and falls back to GitHub if CNB lookup fails.

The response shape is:

```json
{
  "current_version": "0.1.0-beta.16",
  "latest_version": "v0.1.0-beta.17",
  "update_available": true,
  "checked_at": "2026-05-09T10:00:00Z"
}
```

## Frontend Cache Policy

The frontend caches version checks in `localStorage` by current version.

- If a version has never been checked, the UI calls the backend.
- If the last check for that version is less than 1 hour old, the UI reuses the cached result.
- If the running version changes after an update, the cache key changes, so the next page load behaves like a fresh check.
- Failed checks are also cached for 1 hour to avoid repeated upstream/network failures on every page entry.

## UI Placement

Dashboard server status:

- The update icon appears next to the server version in the `运行状态` cell.
- The icon is a small yellow circular alert icon without a custom background.
- Clicking the icon opens the update dialog.

Client detail:

- The old `隧道状态` cell is replaced with `限速状态`.
- First line shows ingress and egress bandwidth limits. Unlimited is shown as `∞`.
- The edit icon opens the same client bandwidth dialog previously opened by the header `限速` button.
- Second line shows the client version plus the same update icon and dialog behavior.

## Update Dialog Modes

The dialog always shows:

- Current version
- Latest version
- Runtime mode

Direct binary / CLI:

- Text: `您当前使用二进制直接运行，请您前往 GitHub 下载最新二进制文件进行更新。`
- The footer button opens `https://github.com/zsio/netsgo/releases`.

Docker:

- Text: `您当前使用 Docker 镜像运行，请前往以下制品页面查看最新镜像。`
- Shows these artifact links:
  - CNB: `https://cnb.cool/zsio/netsgo/-/packages/docker/netsgo`
  - Docker Hub: `https://hub.docker.com/r/zsio/netsgo`
- The footer button also opens `https://github.com/zsio/netsgo/releases`.

Managed service:

- No extra explanatory text is shown.
- The dialog shows a primary `开始更新` action.
- Intended behavior: the service downloads the latest binary package from CNB or GitHub, verifies it, applies the replacement, and restarts itself.

## Remaining Work

The current UI includes the service update action surface, but the actual self-update API still needs backend implementation.

Suggested follow-up API:

```text
POST /api/version/update
```

Expected service-only behavior:

- Reject direct binary and Docker deployments.
- Download the latest compatible binary package from CNB, falling back to GitHub.
- Verify checksum before replacing the installed binary.
- Restart the managed service after replacement.
- Return clear status for started, downloading, verifying, replacing, restarting, completed, and failed phases.
