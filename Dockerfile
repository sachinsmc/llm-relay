# syntax=docker/dockerfile:1

# --- build stage ---
FROM golang:1.26 AS build
WORKDIR /src

# No third-party dependencies, so the module graph is just go.mod.
COPY go.mod ./
RUN go mod download

COPY . .
# Static, stripped binary so it runs on a scratch/distroless base.
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/llm-relay ./cmd/llm-relay

# --- runtime stage ---
# distroless static ships CA certificates (needed for HTTPS to providers) and
# runs as a non-root user.
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/llm-relay /llm-relay
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/llm-relay"]
