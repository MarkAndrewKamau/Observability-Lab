# syntax=docker/dockerfile:1
# One multi-stage build for all Go services, selected by --build-arg SERVICE=.
# Produces a tiny static, non-root, distroless image per service.
ARG GO_VERSION=1.26

FROM golang:${GO_VERSION}-bookworm AS build
ARG SERVICE
WORKDIR /src
# Cache modules first for fast incremental builds.
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" \
    -o /out/app ./services/${SERVICE}/cmd

FROM gcr.io/distroless/static:nonroot
COPY --from=build /out/app /app
USER nonroot:nonroot
ENTRYPOINT ["/app"]
