# syntax=docker/dockerfile:1

FROM --platform=$BUILDPLATFORM golang:1.22-alpine AS build

ARG TARGETOS
ARG TARGETARCH
ARG TARGETVARIANT

WORKDIR /src

RUN apk add --no-cache ca-certificates

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN go generate ./...
RUN if [ -n "$TARGETVARIANT" ]; then export GOARM="${TARGETVARIANT#v}"; fi; \
    CGO_ENABLED=0 GOOS="${TARGETOS:-linux}" GOARCH="${TARGETARCH:-$(go env GOARCH)}" \
    go build -trimpath -ldflags="-s -w" -o /out/pihole-exporter ./cmd/pihole-exporter

FROM scratch

COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=build /out/pihole-exporter /pihole-exporter

USER 65532:65532
EXPOSE 9617
ENTRYPOINT ["/pihole-exporter"]
