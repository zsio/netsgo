<p align="center">
  <img src="web/public/logo.svg" width="80" height="80" alt="NetsGo Logo" />
</p>

<h1 align="center">NetsGo</h1>
<p align="center">
  <strong>Intranet tunneling and node management platform</strong><br/>
  Built-in Web console · Single-port access · Single-file deployment
</p>

<p align="center">
  <strong>English</strong> | <a href="README.zh-CN.md">简体中文</a>
</p>

<p align="center">
  <a href="https://github.com/zsio/netsgo/actions/workflows/ci.yml"><img src="https://github.com/zsio/netsgo/actions/workflows/ci.yml/badge.svg" alt="CI"></a>
  <a href="https://github.com/zsio/netsgo/releases"><img src="https://img.shields.io/github/v/release/zsio/netsgo?include_prereleases&label=release" alt="Release"></a>
  <a href="https://hub.docker.com/r/zsio/netsgo"><img src="https://img.shields.io/badge/docker-zsio%2Fnetsgo-blue?logo=docker" alt="Docker"></a>
</p>

<p align="center">
  <img src="https://img.shields.io/badge/Go-1.25.12-00ADD8?logo=go&logoColor=white" alt="Go">
  <img src="https://img.shields.io/badge/React-19-61DAFB?logo=react&logoColor=black" alt="React">
  <img src="https://img.shields.io/badge/deploy-single--binary-brightgreen" alt="Single Binary">
  <img src="https://img.shields.io/badge/platform-linux%20%7C%20macOS%20%7C%20Windows-lightgrey" alt="Platform">
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-Apache--2.0-blue.svg" alt="License"></a>
</p>

---

**NetsGo** is an out-of-the-box intranet tunneling and node management platform. It combines the Web console, REST API, client access, and low-level network tunnels into a single-file binary, making deployment simpler, access more unified, and operations easier. You can use it to remotely access services inside private networks, or to centrally manage remote nodes distributed across different locations.

---

## Contents

- [Preview](#preview)
- [Quick Start](#quick-start)
- [Why NetsGo](#why-netsgo)
- [How It Differs From Common Alternatives](#how-it-differs-from-common-alternatives)
- [License](#license)

---

## Preview

<p align="center">
  <img src=".github/assets/dashboard.webp" alt="NetsGo Web console overview" width="92%" />
  <br/>
  <sub><strong>Web console overview</strong></sub>
</p>

---

## Quick Start

For more documentation and usage guides, visit the official website: [https://netsgo.zs.uy](https://netsgo.zs.uy).

### One-line install

```bash
curl -fsSL https://netsgo.zs.uy/install.sh | sh
```

### One-line upgrade

```bash
curl -fsSL https://netsgo.zs.uy/upgrade.sh | sh -s -- -y
```

After installation, follow the interactive prompts to initialize a Server or Client. Once the Client is online, you can create and manage tunnels in the Web console.

---

## Why NetsGo

If you want to start the server quickly, connect private-network machines, and then create tunnels, NetsGo focuses on exactly that:

- **One binary is enough**: run `./netsgo server` to start the server, and `./netsgo client` to connect a remote node.
- **One port is enough**: the Web console, control channel, and data channel share the same entry point, so firewalls and reverse proxies only need one shared access path. TCP/UDP tunnels use additional ports only when needed.
- **A console is included by default**: after a client connects, manage nodes, inspect status, and configure tunnels directly in the Web console.

## How It Differs From Common Alternatives

This table is a capability checklist, not a ranking. Cells intentionally use short labels:

- `Built-in`: included in the product itself
- `Platform`: provided by a hosted platform
- `Plugin`: commonly implemented through a plugin or panel
- `Config`: mainly handled through config files or commands
- `External`: typically requires third-party monitoring, logging, or operations systems

| Item | **NetsGo** | **frp** | **ngrok** | **cloudflared** | **rathole** |
|---|---|---|---|---|---|
| Product focus | Self-hosted management platform | Self-hosted tunneling tool | Hosted platform | Cloudflare tunnel | Self-hosted lightweight tunnel |
| Self-hosted server | ✅ | ✅ | ❌ | Platform | ✅ |
| Web UI | Built-in | Built-in/plugin | Platform | Platform | External |
| API / automation | Built-in REST | Supported/plugin | Platform | Platform | Config |
| Client management | Built-in | Config/plugin | Platform | Platform | Config |
| Tunnel CRUD | Web/API | Config/plugin | Platform | Platform | Config |
| Shared Web/API/client entry | ✅ | Multiple entries | Platform | Platform | Config |
| HTTP tunnel | ✅ | ✅ | ✅ | ✅ | TCP-backed |
| TCP tunnel | ✅ | ✅ | ✅ | ✅ | ✅ |
| UDP tunnel | ✅ | ✅ | Plan/scenario-dependent | Scenario-dependent | ✅ |
| Login / key | Admin + Key | Token/OIDC | Account/Token | Account/policy | Token |
| Online status | Built-in | Built-in/plugin | Platform | Platform | Logs/external |
| Traffic statistics | Built-in | Built-in/plugin | Platform | Platform | External |
| Reconnect after disconnect | ✅ | ✅ | ✅ | ✅ | ✅ |
| Rate limiting | Built-in | Built-in | Platform/plan | Platform policy | External |

### Usage Guidance

- **NetsGo**: for scenarios where you want to manage clients, tunnels, status, and traffic in one self-hosted Web console.
- **frp / rathole**: for configuration-driven, lightweight deployments where you compose your own operations tooling.
- **ngrok / cloudflared**: for hosted-platform scenarios where you want to use platform accounts and edge network capabilities directly.

## License

[Apache-2.0](LICENSE)

First published on the [linux.do](https://linux.do) forum.
