# NetsGo Desktop

NetsGo Desktop is the Tauri client wrapper. It embeds the React UI and bundles the `netsgo client` sidecar built for the same Rust target triple.

## Local Development

```bash
make build-desktop-sidecar
cd desktop
bun install --frozen-lockfile
bun run tauri dev
```

## Build

Compile the desktop app without producing an installer:

```bash
make build-desktop
```

Build a macOS `.app` for local testing:

```bash
make build-desktop-macos-local
```

This creates:

```text
desktop/src-tauri/target/<target-triple>/release/bundle/macos/NetsGo.app
```

Package that `.app` into a DMG:

```bash
make package-desktop-macos-local
```

## macOS Signing Without an Apple Developer Account

`build-desktop-macos-local` signs the app with an ad-hoc identity by default:

```bash
codesign --sign -
```

This keeps the local bundle internally consistent, including the sidecar binary, but it does not make Gatekeeper trust the app after it is downloaded from the internet. Test users may still need to remove the quarantine attribute after copying the app out of the DMG:

```bash
xattr -dr com.apple.quarantine NetsGo.app
```

For a Developer ID certificate later, reuse the same target with:

```bash
make build-desktop-macos-local DESKTOP_CODESIGN_IDENTITY="Developer ID Application: Example Inc (TEAMID)"
```

Notarization is still required for a normal double-click installation experience for public macOS distribution.
