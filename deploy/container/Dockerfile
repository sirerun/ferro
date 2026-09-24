# The Go builder is pinned to the official golang:1.25.5-alpine multi-platform
# image index digest verified on Docker Hub on 2026-09-24.
FROM golang:1.25.5-alpine@sha256:ac09a5f469f307e5da71e766b0bd59c9c49ea460a528cc3e6686513d64a6f1fb AS build

ARG TARGETOS
ARG TARGETARCH
ENV GOMAXPROCS=2
ENV GOFLAGS=-p=2

WORKDIR /src
RUN apk add --no-cache ca-certificates
COPY go.mod go.sum ./
RUN GOWORK=off go mod download
COPY . .
RUN mkdir -p /out /var/lib/ferro-cloud && chown 65532:65532 /var/lib/ferro-cloud
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} GOWORK=off \
    go build -trimpath -ldflags='-s -w -buildid=' -o /out/ferro-cloud ./cmd/ferro-cloud

FROM scratch
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build --chown=65532:65532 /out/ferro-cloud /usr/local/bin/ferro-cloud
COPY --from=build --chown=65532:65532 /var/lib/ferro-cloud /var/lib/ferro-cloud
ENV FERRO_MCP_HOME=/var/lib/ferro-cloud
EXPOSE 8080
USER 65532:65532
ENTRYPOINT ["/usr/local/bin/ferro-cloud"]
