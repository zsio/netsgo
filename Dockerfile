# syntax=docker/dockerfile:1.7

FROM --platform=$BUILDPLATFORM golang:1.25.5-bookworm AS builder

ARG TARGETOS
ARG TARGETARCH
ARG TARGETVARIANT
ARG NETSGO_VERSION=dev
ARG NETSGO_COMMIT=unknown
ARG NETSGO_DATE=unknown

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY cmd ./cmd
COPY internal ./internal
COPY pkg ./pkg
COPY web/embed.go ./web/embed.go
COPY web/embed_dev.go ./web/embed_dev.go
COPY web/dist ./web/dist

RUN set -eux; \
    goarm=""; \
    if [ "${TARGETARCH}" = "arm" ] && [ "${TARGETVARIANT}" = "v7" ]; then \
        goarm="7"; \
    fi; \
    export CGO_ENABLED=0 GOOS="${TARGETOS}" GOARCH="${TARGETARCH}"; \
    if [ -n "${goarm}" ]; then \
        export GOARM="${goarm}"; \
    fi; \
    go build \
        -trimpath \
        -ldflags="-s -w -X netsgo/pkg/version.Current=${NETSGO_VERSION} -X netsgo/pkg/version.Commit=${NETSGO_COMMIT} -X netsgo/pkg/version.Date=${NETSGO_DATE}" \
        -o /out/netsgo \
        ./cmd/netsgo

FROM gcr.io/distroless/base-debian12:nonroot

WORKDIR /app

COPY --from=builder /out/netsgo /usr/local/bin/netsgo

EXPOSE 8080

ENV NETSGO_PORT=8080

ENTRYPOINT ["/usr/local/bin/netsgo"]
CMD ["server"]

FROM alpine:3.21 AS e2e

RUN apk add --no-cache ca-certificates curl jq

WORKDIR /app

COPY --from=builder /out/netsgo /usr/local/bin/netsgo
COPY test/e2e/scripts /opt/netsgo-e2e

RUN chmod +x /opt/netsgo-e2e/*.sh

ENV NETSGO_PORT=8080

ENTRYPOINT ["/usr/local/bin/netsgo"]
CMD ["server"]
