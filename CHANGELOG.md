# Changelog

All notable changes to this project will be documented in this file.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

<!-- Add a new entry for each release. The release workflow checks that an entry matching the tag exists. -->

## [v0.1.0-alpha] - 2026-05-23

### Added

- Pi-hole v6 API client with session-based authentication and TOTP support
- Prometheus metrics collector exposing Pi-hole statistics (queries, blocking, cache, clients, upstream resolvers)
- `/healthz` endpoint reporting exporter scrape health
- OpenTelemetry metrics exporter support via `OTEL_METRICS_EXPORTER` environment variable
- Docker image published to GitHub Container Registry (`ghcr.io`) targeting `linux/arm64` (Raspberry Pi 4 64-bit)
- Release-triggered CI workflow for building and pushing the container image via Podman
- Grafana dashboard JSON for visualising Pi-hole metrics
