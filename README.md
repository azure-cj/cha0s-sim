# cha0s;sim

A local reverse proxy that injects real faults into real HTTP traffic, to test
frontend resilience.

**Windows desktop app:** download the installer from
[GitHub Releases](https://github.com/azure-cj/cha0s-sim/releases) — or
[build from source](#building-from-source).

## What is this?

cha0s;sim is a local reverse proxy you run in front of your own backend. It
injects real faults into real HTTP traffic — latency, faked errors, dropped
connections, corrupted payloads, and malicious-looking input — so you can watch
how your frontend actually behaves under failure, then switch it back off.
There are no simulated services: every request is forwarded to your real
backend. It's a desktop console (Wails + Go + React) with a headless CLI that
drives the same rule engine.

## Features

- **Chaos injection** — five named effect types per rule: `latency`,
  `status_override`, `drop_connection`, `mangle`, and `fuzz`. Rules are
  hot-reloaded from `chaos.yaml`/JSON, gated by `error_rate`, and target a
  path or regex.
- **Stress testing** — flat (the default), ramp (`continuous`), stepped, and
  spike load shapes, with p50/p95/p99 latency reporting, run headlessly or
  from the desktop app.
- **Multi-step scenarios** — chain requests so one step's response feeds the
  next (`{{variable}}` extraction from JSON responses). Scenario files are
  YAML/JSON and are currently CLI-only (`stress scenario --file`).
- **No-YAML rule builder** — the desktop wizard lists endpoints discovered
  passively from traffic your app has already sent through the proxy, so you
  can pick one and create a rule without hand-writing YAML.
- **Chaos session on a fixed port** — the proxy listens on one fixed port
  (8081 in the desktop app) and tags every event with its session name, so the
  metrics console and the log stay scoped to that session.
- **Desktop dashboard** — a live event feed, the Chaos Flow traffic
  visualization, an endpoint registry, a stress/chaos metrics console, and a
  shareable "Damage Control Certificate" scorecard in a single Wails app.

## What changed

The **Security** scanning session from v0.1.0 has been removed. cha0s;sim is now
chaos-only: the passive scan of response headers and bodies for missing security
headers and leaked secrets is gone, along with its `security` session (port
8082) and its dashboard view. The v0.1.0 GitHub release still contains that
feature; this removal ships in the next release.

## Screenshots

<img width="1001" height="755" alt="image" src="https://github.com/user-attachments/assets/52f149dd-9210-42be-bba4-6d6fcacf5462" />


<img width="998" height="725" alt="image" src="https://github.com/user-attachments/assets/888c00b4-8a64-4cf8-a4dc-01ca5a11d210" />
<img width="997" height="721" alt="image" src="https://github.com/user-attachments/assets/7a160a85-75fe-4537-b995-8b131f1e37ca" />


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

In the desktop app: open Settings and point the app at your backend URL,
create rules via the wizard, then start the chaos session (fixed port 8081) and
watch the live feed, Chaos Flow, and metrics on the dashboard.

## Project status

Cha0s-sim is a working local tool. The chaos and stress engines are
covered by the Go test suite and are driven from both the CLI and the desktop
app. The desktop GUI is verified manually rather than by automated UI tests, and
the project is Windows-focused.

The desktop app is distributed as an unsigned NSIS installer; there is no
code-signing certificate, hence the SmartScreen warning described in
[Installation](#installation). The CLI is not packaged and must be built from
source. Multi-step scenarios are currently CLI-only; the desktop app does not
surface them yet. Compiled binaries are intentionally not committed — they are
attached to GitHub Releases as assets instead (see `.gitignore`).

## Known limitations

- **Windows only** — the desktop app renders through the Edge WebView2 runtime
  and ships as a Windows installer.
- **Unsigned builds** — there is no code-signing certificate, so Windows
  SmartScreen warns on the installer (see
  [Installation](#installation)).
- **No auto-update** — you update by downloading a new installer from the
  [Releases page](https://github.com/azure-cj/cha0s-sim/releases).
- **Multi-step scenarios are CLI-only** — the desktop app does not surface them
  yet (`stress scenario --file`).
- **Copy-as-cURL captures the method and URL only** — traffic events do not
  record request headers, body, or query string.
- **No automated GUI tests** — the Go test suite covers the engines; the desktop
  UI is verified manually.

## License

MIT — see [LICENSE](LICENSE).
