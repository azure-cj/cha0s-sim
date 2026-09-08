# cha0s;sim

Break your own API on purpose. cha0s;sim sits as a local proxy in front of
your backend and lets you flip chaos on, watch it hit your app in real time,
and flip it back off — from a small desktop console (Wails + Go + React).

## How it works

Point both sessions at your target URL, then route your app's traffic through
the proxy instead of hitting it directly:

| Session | Port | What it does |
|---|---|---|
| Chaos | `8081` | Injects latency, error status codes, dropped connections, corrupted JSON, and fuzz payloads per rule |
| Security | `8082` | Passes traffic through and scans responses for missing/weak security headers and leaked secrets |

Every request and finding flows into a live feed tagged with its session, so
you always know which side of the proxy an event came from.

## Chaos rules

Rules live in `chaos.yaml` (or JSON) and are hot-reloaded — no restarts. Each
rule targets a path or regex, is gated by an `error_rate`, and applies one
effect: `latency`, `status_override`, `drop_connection`, `mangle`, or `fuzz`.

## Also includes

- Smart suggestions that turn observed failures into new rules with one click
- A sandbox scorecard summarizing events, findings, and stress-test metrics
- Built-in stress testing with percentile latencies (p50/p95/p99)
- A plain reverse-proxy CLI (`cha0s-sim --target http://...)`) for headless or
  scripting use, driven by the same YAML rules

## Build & run

```sh
# Desktop app
wails build
./build/bin/cha0s-sim-desktop.exe

# Headless proxy
go run ./cmd/cha0s-sim --target http://localhost:3000
```

Point your app at `http://localhost:8081` (chaos) or `http://localhost:8082`
(security) while developing.