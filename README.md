# cha0s;sim

A local chaos-injection and security-scanning proxy for testing frontend
resilience.

**Windows desktop app:** download the installer from
[GitHub Releases](https://github.com/azure-cj/cha0s-sim/releases) — or
[build from source](#building-from-source).

## What is this?

cha0s;sim is a local proxy you run in front of your own backend so you can
break the stuff that eats services in production — chaos effects, dropped and
dead-slow responses, missing security headers, leaked secrets — and watch it
hit your app for real, then flip it back off. It's a desktop console (Wails
+ Go + React) with a headless CLI that drives the same rule engine.

## Features

- **Chaos injection** — five named effect types per rule: `latency`,
  `status_override`, `drop_connection`, `mangle`, and `fuzz`. Rules are
  hot-reloaded from `chaos.yaml`/JSON, gated by `error_rate`, and target a
  path or regex.
- **Passive security scanning** — the `security` session passes traffic
  through and flags missing/weak security headers and leaked secrets, with
  per-finding remediation suggestions.
- **Stress testing** — flat (the default), ramp (`continuous`), stepped, and
  spike load shapes, with p50/p95/p99 latency reporting, run headlessly or
  from the desktop app.
- **Multi-step scenarios** — chain requests so one step's response feeds the
  next (`{{variable}}` extraction from JSON responses). Scenario files are
  YAML/JSON and are currently CLI-only (`stress scenario --file`).
- **No-YAML rule builder** — the desktop wizard lists endpoints discovered
  passively from traffic your app has already sent through the proxy, so you
  can pick one and create a rule without hand-writing YAML.
- **Session isolation** — Chaos and Security run on separate ports/sessions
  with separate metric tracks; each event is tagged with its session.
- **Desktop dashboard** — a live event feed, endpoint registry, and a
  stress/chaos metrics console in a single Wails app.

## Screenshots

<!-- Add a screenshot of the Dashboard here. The image paths below are
     placeholders: no image files exist in the repo yet. Capture the running
     app, drop the PNGs into docs/screenshots/, and they render as-is. -->

![Dashboard](docs/screenshots/dashboard.png)

<!-- Add a screenshot of the Chaos rule wizard here. -->

![Rule wizard](docs/screenshots/rule-wizard.png)

## Installation

The desktop app is **Windows only** — it is built with Wails and renders
through the Edge WebView2 runtime.

1. Download `cha0s-sim-desktop-amd64-installer.exe` from the
   [Releases page](https://github.com/azure-cj/cha0s-sim/releases).
2. Run it. The installer creates both a **Desktop shortcut** and a **Start
   Menu entry**, and registers cha0s;sim under *Add or remove programs* so it
   uninstalls normally.
3. Launch it, open **Settings**, and point the app at your backend URL.

> **Windows SmartScreen will likely warn you that the publisher is unknown.**
> This is expected. cha0s;sim is an independent open-source project with no
> paid code-signing certificate, so the installer is **unsigned** — Windows
> flags all such binaries. If you trust the source, choose
> *More info → Run anyway*. On Windows 11 you can also right-click the
> downloaded `.exe` and pick *Properties → Unblock*, then run it.

**The installer ships the desktop app only.** The headless `cha0s-sim` CLI is
not included in it — CLI users need to [build it from
source](#building-from-source).

## Building requirements

Only needed if you are building from source. Installing the released installer
requires none of these.

- Go 1.27.0 (see `go.mod`)
- Node.js + npm (version not pinned in `package.json`; Wails frontend toolchain
  builds with Vite 7)
- Wails CLI v2 (install via `go install github.com/wailsapp/wails/v2/cmd/wails@latest`)
- Microsoft Edge WebView2 Runtime (Windows; preinstalled on Windows 11)

## Building from source

Use this path if you want to run from a checkout, contribute, or build the CLI.
If you just want to use the app, use the [installer](#installation) instead.

```sh
git clone https://github.com/azure-cj/cha0s-sim.git
cd cha0s-sim

# Headless CLI (proxy + stress + scenario). Needs no frontend.
go build -o cha0s-sim.exe ./cmd/cha0s-sim
./cha0s-sim.exe --target http://localhost:3000

# Desktop app. `wails build` runs `npm install` and `npm run build` inside
# frontend/ for you (see wails.json). To do the frontend step by hand:
#   cd frontend && npm install && npm run build && cd ..
wails build
./build/bin/cha0s-sim-desktop.exe
```

## Usage

```sh
# Headless proxy: route your app through it
cha0s-sim --target http://localhost:3000 --port 8080

# Stress test with a stepped load shape
cha0s-sim stress --target http://localhost:3000 --shape stepped \
  --rps 10 --duration 60s

# Multi-step scenario from a YAML file (see configs/scenario.example.yaml)
cha0s-sim stress scenario --file configs/scenario.example.yaml \
  --target http://localhost:3000 --vus 3 --duration 30s
```

In the desktop app: open Settings and point the app at your backend URL (the
chaos and security sessions share it and each listens on its own fixed port),
create rules via the wizard, then start a session and watch the live feed and
metrics on the dashboard.

## Project status

Cha0s-sim is a working local tool. The chaos, security, and stress engines are
covered by the Go test suite and are driven from both the CLI and the desktop
app. The desktop GUI is verified manually rather than by automated UI tests, and
the project is Windows-focused.

The desktop app is distributed as an unsigned NSIS installer; there is no
code-signing certificate, hence the SmartScreen warning described in
[Installation](#installation). The CLI is not packaged and must be built from
source. Multi-step scenarios are currently CLI-only; the desktop app does not
surface them yet. Compiled binaries are intentionally not committed — they are
attached to GitHub Releases as assets instead (see `.gitignore`).

## License

MIT — see [LICENSE](LICENSE).
