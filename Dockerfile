FROM golang:1.22-alpine AS build

WORKDIR /src

RUN apk add --no-cache ca-certificates

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN go generate ./...
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/pihole-exporter ./cmd/pihole-exporter

FROM scratch

COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=build /out/pihole-exporter /pihole-exporter

USER 65532:65532
EXPOSE 9617
ENTRYPOINT ["/pihole-exporter"]
