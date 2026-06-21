# syntax=docker/dockerfile:1

# --- build stage ---
# Build on the native platform and cross-compile to the target arch (pure Go,
# CGO disabled), so multi-arch builds don't need slow QEMU emulation.
FROM --platform=$BUILDPLATFORM golang:1.26 AS build
WORKDIR /src

# No third-party dependencies, so the module graph is just go.mod.
COPY go.mod ./
RUN go mod download

COPY . .
ARG TARGETOS TARGETARCH
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags="-s -w" -o /out/llm-relay ./cmd/llm-relay

# --- runtime stage ---
# distroless static ships CA certificates (needed for HTTPS to providers) and
# runs as a non-root user.
FROM gcr.io/distroless/static-debian12:nonroot
LABEL org.opencontainers.image.source="https://github.com/sachinsmc/llm-relay"
LABEL org.opencontainers.image.description="OpenAI-compatible LLM gateway in Go with provider failover and SSE streaming."
LABEL org.opencontainers.image.licenses="MIT"
COPY --from=build /out/llm-relay /llm-relay
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/llm-relay"]
