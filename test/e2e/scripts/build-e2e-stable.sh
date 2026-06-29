#!/usr/bin/env bash
set -euo pipefail

TAG="${1:?Usage: build-e2e-stable.sh <tag> <image-name>}"
IMAGE_NAME="${2:-netsgo-e2e:${TAG}}"

REPO_ROOT="$(git rev-parse --show-toplevel)"

log() { echo "[build-e2e-stable] $*"; }

detect_default_platform() {
	local platform
	platform="$(docker version --format '{{.Server.Os}}/{{.Server.Arch}}' 2>/dev/null || true)"
	case "${platform}" in
		linux/amd64|linux/arm64|linux/arm/v7)
			echo "${platform}"
			;;
		*)
			echo "linux/amd64"
			;;
	esac
}

E2E_PLATFORM="${E2E_PLATFORM:-$(detect_default_platform)}"

log "building image '${IMAGE_NAME}' from git tag '${TAG}' (platform=${E2E_PLATFORM})"

# ---------- Tool version diagnostics ----------
if command -v bun >/dev/null 2>&1; then
	log "bun version: $(bun --version)"
elif command -v npm >/dev/null 2>&1; then
	log "npm version: $(npm --version)"
	log "node version: $(node --version 2>/dev/null || echo unknown)"
else
	log "WARNING: neither bun nor npm found; frontend build will fail if web/package.json exists"
fi

# ---------- Extract source tree from tag ----------
tmpdir="$(mktemp -d)"
cleanup() { rm -rf "${tmpdir}"; }
trap cleanup EXIT

log "extracting source tree at ${TAG}..."
if ! git archive --format=tar "${TAG}" | tar -x -C "${tmpdir}"; then
	log "ERROR: failed to extract git tag ${TAG}"
	log "  Ensure the tag exists: git tag -l '${TAG}'"
	exit 1
fi

if [ ! -d "${tmpdir}/web" ]; then
	log "ERROR: tag ${TAG} does not contain web/ directory"
	exit 1
fi

# ---------- Build frontend ----------
if [ -f "${tmpdir}/web/package.json" ]; then
	log "building frontend at tag ${TAG}..."
	if command -v bun >/dev/null 2>&1; then
		if ! (cd "${tmpdir}/web" && bun install --frozen-lockfile && bun run build); then
			log "ERROR: bun run build failed for web/ at tag ${TAG}"
			log "  This usually means the frontend at this tag has build issues or missing deps."
			log "  Try: git checkout ${TAG} && cd web && bun install && bun run build"
			exit 1
		fi
	elif command -v npm >/dev/null 2>&1; then
		if ! (cd "${tmpdir}/web" && npm ci && npm run build); then
			log "ERROR: npm run build failed for web/ at tag ${TAG}"
			log "  This usually means the frontend at this tag has build issues or missing deps."
			log "  Try: git checkout ${TAG} && cd web && npm ci && npm run build"
			exit 1
		fi
	else
		log "ERROR: bun or npm is required to build frontend at tag ${TAG}"
		exit 1
	fi
	if [ ! -d "${tmpdir}/web/dist" ]; then
		log "ERROR: web/dist was not produced after frontend build at tag ${TAG}"
		exit 1
	fi
fi

# ---------- Build Docker image ----------
log "running docker buildx build --target e2e --platform ${E2E_PLATFORM}..."
if ! docker buildx build --load \
	--platform "${E2E_PLATFORM}" \
	--target e2e \
	--build-arg "NETSGO_VERSION=${TAG}" \
	--build-arg "NETSGO_COMMIT=$(git rev-parse --short "${TAG}" 2>/dev/null || echo "unknown")" \
	--build-arg "NETSGO_DATE=unknown" \
	-t "${IMAGE_NAME}" \
	"${tmpdir}"; then
	log "ERROR: docker buildx build failed for tag ${TAG}"
	log "  Check that the Dockerfile 'e2e' target can build from this tag's source."
	exit 1
fi

log "image ready: ${IMAGE_NAME}"
