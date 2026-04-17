# Pi-hole Exporter

Prometheus exporter for Pi-hole v6 metrics.

The exporter authenticates against the Pi-hole API, scrapes the compiled Pi-hole metrics endpoints, and exposes them in Prometheus format on `/metrics`.

## Features

- Supports Pi-hole API `6.0`
- Authenticates with a Pi-hole app password through `/api/auth`
- Reuses and refreshes Pi-hole sessions automatically
- Exposes Prometheus metrics on `/metrics`
- Exposes a simple health check on `/healthz`
- Ships with a multi-stage Dockerfile that builds a static scratch image

## Requirements

- Go 1.22 or newer
- Pi-hole v6 with API access enabled
- A Pi-hole app password

## Configuration

Configuration can be supplied with flags or environment variables.

| Flag | Environment variable | Default | Description |
| --- | --- | --- | --- |
| `-listen` | `LISTEN_ADDR` | `:9617` | HTTP listen address |
| `-pihole-url` | `PIHOLE_BASE_URL` | required | Pi-hole base URL, including scheme and host |
| `-password` | `PIHOLE_APP_PASSWORD` | required | Pi-hole app password |
| `-timeout` | `SCRAPE_TIMEOUT` | `10s` | Timeout for each Pi-hole scrape |

`SCRAPE_TIMEOUT` accepts Go duration strings such as `5s`, `30s`, or `1m`. Plain integer values are treated as seconds.

The exporter removes `PIHOLE_APP_PASSWORD` from its process environment after configuration is parsed. The password still remains in process memory because the exporter needs it to authenticate and refresh Pi-hole sessions.

## Run Locally

```sh
export PIHOLE_BASE_URL="http://192.168.0.2"
export PIHOLE_APP_PASSWORD="your-app-password"

go run ./cmd/pihole-exporter
```

The exporter listens on `:9617` by default:

```sh
curl http://localhost:9617/healthz
curl http://localhost:9617/metrics
```

You can also pass configuration as flags:

```sh
go run ./cmd/pihole-exporter \
  -listen ":9617" \
  -pihole-url "http://192.168.0.2" \
  -password "your-app-password" \
  -timeout 10s
```

## Docker

Run the published image from Docker Hub:

```sh
docker run --rm -p 9617:9617 \
  -e PIHOLE_BASE_URL="http://192.168.0.2" \
  -e PIHOLE_APP_PASSWORD="your-app-password" \
  alantoch/pihole-exporter:latest
```

The exporter is then available at:

```sh
curl http://localhost:9617/healthz
curl http://localhost:9617/metrics
```

Build the image:

```sh
docker build -t pihole-exporter .
```

Build and push a multi-architecture image for AMD64 and ARM64:

```sh
docker buildx build \
  --platform linux/amd64,linux/arm64 \
  -t alantoch/pihole-exporter:latest \
  --push .
```

Run the locally built image:

```sh
docker run --rm -p 9617:9617 \
  -e PIHOLE_BASE_URL="http://192.168.0.2" \
  -e PIHOLE_APP_PASSWORD="your-app-password" \
  pihole-exporter
```

## Docker Compose

Create a `.env` file next to `compose.yaml`:

```sh
PIHOLE_BASE_URL=http://192.168.0.2
PIHOLE_APP_PASSWORD=your-app-password
```

Run only the exporter:

```yaml
services:
  pihole-exporter:
    image: alantoch/pihole-exporter:latest
    restart: unless-stopped
    ports:
      - "9617:9617"
    environment:
      PIHOLE_BASE_URL: ${PIHOLE_BASE_URL}
      PIHOLE_APP_PASSWORD: ${PIHOLE_APP_PASSWORD}
```

Start it:

```sh
docker compose up -d
```

## Prometheus

If Prometheus runs outside Docker, scrape the published host port:

```yaml
scrape_configs:
  - job_name: pihole
    static_configs:
      - targets:
          - localhost:9617
```

If Prometheus runs in the same Docker Compose project as the exporter, use the service name:

```yaml
scrape_configs:
  - job_name: pihole
    static_configs:
      - targets:
          - pihole-exporter:9617
```

Example `compose.yaml` with Prometheus:

```yaml
services:
  pihole-exporter:
    image: alantoch/pihole-exporter:latest
    restart: unless-stopped
    ports:
      - "9617:9617"
    environment:
      PIHOLE_BASE_URL: ${PIHOLE_BASE_URL}
      PIHOLE_APP_PASSWORD: ${PIHOLE_APP_PASSWORD}

  prometheus:
    image: prom/prometheus:latest
    restart: unless-stopped
    ports:
      - "9090:9090"
    volumes:
      - ./prometheus.yml:/etc/prometheus/prometheus.yml:ro
```

Use this `prometheus.yml` next to `compose.yaml`:

```yaml
global:
  scrape_interval: 30s

scrape_configs:
  - job_name: pihole
    static_configs:
      - targets:
          - pihole-exporter:9617
```

## Grafana Alloy

If Alloy runs in the same Docker Compose project as the exporter, scrape the exporter with `prometheus.scrape` and forward the metrics to any Prometheus remote write compatible endpoint.

For local Prometheus, enable the remote write receiver on the Prometheus container:

```yaml
services:
  prometheus:
    image: prom/prometheus:latest
    command:
      - --config.file=/etc/prometheus/prometheus.yml
      - --web.enable-remote-write-receiver
    ports:
      - "9090:9090"
```

Then use this Alloy configuration:

```river
prometheus.scrape "pihole" {
  targets = [
    {"__address__" = "pihole-exporter:9617"},
  ]

  forward_to      = [prometheus.remote_write.default.receiver]
  scrape_interval = "30s"
}

prometheus.remote_write "default" {
  endpoint {
    url = "http://prometheus:9090/api/v1/write"
  }
}
```

For Grafana Cloud, use your remote write URL and credentials:

```river
prometheus.scrape "pihole" {
  targets = [
    {"__address__" = "pihole-exporter:9617"},
  ]

  forward_to      = [prometheus.remote_write.grafana_cloud.receiver]
  scrape_interval = "30s"
}

prometheus.remote_write "grafana_cloud" {
  endpoint {
    url = "https://prometheus-prod-xx-xxx.grafana.net/api/prom/push"

    basic_auth {
      username = "your-instance-id"
      password = "your-api-token"
    }
  }
}
```

Example `compose.yaml` with Alloy:

```yaml
services:
  pihole-exporter:
    image: alantoch/pihole-exporter:latest
    restart: unless-stopped
    ports:
      - "9617:9617"
    environment:
      PIHOLE_BASE_URL: ${PIHOLE_BASE_URL}
      PIHOLE_APP_PASSWORD: ${PIHOLE_APP_PASSWORD}

  alloy:
    image: grafana/alloy:latest
    restart: unless-stopped
    command:
      - run
      - /etc/alloy/config.alloy
    volumes:
      - ./config.alloy:/etc/alloy/config.alloy:ro
```

## Metrics

The exporter generates metric definitions from the Pi-hole OpenAPI Metrics schemas and currently compiles metrics for Pi-hole API `6.0`.

In addition to Pi-hole metrics, the exporter emits:

| Metric | Description |
| --- | --- |
| `pihole_exporter_build_info` | Exporter build information with the compiled Pi-hole API version |
| `pihole_exporter_scrape_duration_seconds` | Duration of the last Pi-hole scrape in seconds |
| `pihole_exporter_scrape_success` | Whether the last Pi-hole scrape succeeded, where `1` is success and `0` is failure |

Pi-hole metrics include:

| Metric | Labels |
| --- | --- |
| `pihole_query_types_by_type` | `type` |
| `pihole_summary_clients_active` | |
| `pihole_summary_clients_total` | |
| `pihole_summary_gravity_domains_being_blocked` | |
| `pihole_summary_gravity_last_update_timestamp_seconds` | |
| `pihole_summary_queries_blocked` | |
| `pihole_summary_queries_cached` | |
| `pihole_summary_queries_forwarded` | |
| `pihole_summary_queries_frequency` | |
| `pihole_summary_queries_percent_blocked` | |
| `pihole_summary_queries_by_reply` | `reply` |
| `pihole_summary_queries_by_status` | `status` |
| `pihole_summary_queries_total` | |
| `pihole_summary_queries_by_type` | `type` |
| `pihole_summary_queries_unique_domains` | |
| `pihole_top_clients_blocked_queries` | |
| `pihole_top_clients_total_queries` | |
| `pihole_top_domains_blocked_queries` | |
| `pihole_top_domains_total_queries` | |
| `pihole_upstreams_forwarded_queries` | |
| `pihole_upstreams_total_queries` | |

## Development

Run tests:

```sh
go test ./...
```

Run live authentication tests against a real Pi-hole:

```sh
export PIHOLE_BASE_URL="http://192.168.0.2"
export PIHOLE_APP_PASSWORD="your-app-password"
export PIHOLE_LIVE_TEST=1

go test ./pkg/pihole -run TestLiveAuth
```

Regenerate compiled metric definitions from the upstream Pi-hole OpenAPI spec:

```sh
go generate ./...
```

The generator is implemented in `tools/pihole-metricgen` and writes `pkg/pihole/metrics_gen.go`.
