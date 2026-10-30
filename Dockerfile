# syntax=docker/dockerfile:1.7
FROM golang:1.27.3-bookworm AS builder
WORKDIR /src
ENV GOMAXPROCS=2 CGO_ENABLED=0
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    go build -p 1 -trimpath -o /out/api ./cmd/api && \
    go test -p 1 -c -o /out/api-tests ./internal/api

FROM scratch AS binaries
COPY --from=builder /out/ /

FROM debian:bookworm-slim AS base
RUN groupadd --gid 10001 app && useradd --uid 10001 --gid app --no-create-home app
WORKDIR /app
USER 10001:10001

FROM base AS tests
COPY --from=builder /out/api-tests /app/api-tests
CMD ["/app/api-tests", "-test.v", "-test.parallel=1", "-test.timeout=120s"]

FROM base AS runtime
COPY --from=builder /out/api /app/api
EXPOSE 8080
HEALTHCHECK --interval=10s --timeout=5s --start-period=10s --retries=5 CMD ["/app/api", "health"]
ENTRYPOINT ["/app/api"]
