# cha0s;sim Desktop — Design Spec

Status: proposal for review (no code landed yet)
Scope: visual identity, layout, component structure, and data contracts for the
desktop app (Wails + React/TS). Follow-up implementation phases are listed at
the end.

---

## 1. North star

cha0s;sim should feel like the **damage-control console of a dev-only ops room**:
glanceable, technically credible, a little irreverent. The user is a developer
actively coding against a backend; they open this window to flip chaos on/off,
watch it hit their app in real time, and flip it back off. Speed of perception
matters more than fidelity of configuration.

Three working principles:

1. **Status is the hero.** Running/Stopped, live rule count, and the traffic
   feed are the first things the eye lands on. Settings are a drawer, not a
   landing page.
2. **Chaos is color-coded at a glance.** Each failure mode has a signature
   color so a single traffic row (or rule row) tells you *what broke* before
   you read any words.
3. **No form-heavy setup.** Defaults ship sane; configuration is secondary
   and visible only when needed. Onboarding exists for the first run only.

---

## 2. Visual identity

### 2.1 Branding concept

The wordmark reuses the leetspeak: lowercase `cha0s;sim` in a monospace face,
with the **zero rendered as a target reticle / crosshair** (circle with four
quarter ticks) — "we aim chaos at your API". The semicolon is styled as a
"splice" glyph (a broke bar / double-height tick) to hint at corrupted code.

App icon concept: a crosshair with one tick segment sheared off-line (the
"miss" — controlled chaos). No assets are built in this spec — this is the
direction for a future asset pass.

### 2.2 Palette

Built around the existing default navy so the window background
`rgb(27,38,54)` stays in-family.

Surface (dark, blueprint-lab feel):

| Token | Hex | Use |
|---|---|---|
| `--c-bg-950` | `#0B1220` | window/root background |
| `--c-bg-900` | `#1B2636` | primary panel (matches current default) |
| `--c-bg-850` | `#223049` | raised panel / hover |
| `--c-bg-800` | `#2A3A59` | active row / dropdown |
| `--c-line`   | `#32446A` | 1px borders, dividers |

Ink (text, on dark):

| Token | Hex | Use |
|---|---|---|
| `--c-ink-max` | `#EDF2FA` | primary text (headings, status) |
| `--c-ink`     | `#B6C2D9` | body text |
| `--c-ink-dim` | `#7C8BA6` | secondary labels, timestamps |

Accent + chaos severity (each chaos type claims a color — this is the system's
signature move):

| Token | Hex | Meaning |
|---|---|---|
| `--c-accent`   | `#7DF9FF` | brand "signal" cyan; focus rings, primary actions |
| `--c-ok`       | `#34D399` | healthy/passthrough traffic (mirrors proxy logger green 2xx) |
| `--c-latency`  | `#F59E0B` | amber — time/blob delay (latency) |
| `--c-override` | `#EF4444` | red — forced error status (mirrors 5xx) |
| `--c-drop`     | `#C084FC` | violet — severed connection |
| `--c-mangle`   | `#22D3EE` | cyan — payload corruption (mirrors 3xx-ish/glitch) |
| `--c-warn`     | `#FDE047` | warnings (disabled rules, config load failures) |

Chaos colors are used as a left-edge accent bar + a small glyph on traffic rows,
and as the rule-type chips in the Rules list. This keeps the traffic feed
scannable without reading.

> Note: these mirror the existing ANSI colors in `internal/logger/logger.go`
> (green 2xx, cyan 3xx, yellow 4xx, red 5xx) so console output and the UI speak
> the same language.

Contrast: all text pairs above meet WCAG AA on their surface (verified roughly:
`#B6C2D9` on `#1B2636` ≈ 8.5:1).

### 2.3 Typography

- **UI text**: system stack — `Segoe UI` (Windows) / `-apple-system` → no
  bundled font dependency, crisp in a native window:
  `system-ui, -apple-system, "Segoe UI", Ubuntu, Cantarell, sans-serif`.
- **Data / code** (paths, target URLs, traffic rows, method/path, ports):
  `"JetBrains Mono", "Cascadia Code", Consolas, ui-monospace, monospace`.
- **Brand wordmark**: monospace, medium weight, letterspaced; the reticle-zero
  replaces the "0".
- Type scale (rem, base 16px): `10/11` data timestamps, `12` labels, `13` body,
  `15` section titles, `18` panel headers, `24` window/dashboard hero.

Glyph/icon set: thin-stroke, 1.5px line icons (Stroke-style). Core set:
crosshair (brand), play/stop (proxy control), pulse/activity (feed), sliders
(settings), list/toggle (rules), warning, chevron, plus.

### 2.4 Spacing, radius, motion

- Grid: 4px base; panel padding `16px`, section gaps `12px`, inner `8px`.
- Radius: `6px` corners (controls/rows), `8px` panels, `10px` modal/sheet.
- Border: `1px solid var(--c-line)`; elevated surfaces get `0 8px 24px
  rgba(0,0,0,0.35)`.
- Motion: `120–180ms ease-out` for toggles/panels; the live feed animates only
  the newly-appended row (one 150ms fade/slide) — constant animation in a live
  feed is noise.
- Window: keep the native frame + fixed-ish `1024×768` for now; frameless is an
  explicit future option, not this pass.

---

## 3. Information architecture / navigation

Three top-level areas, persistent **left sidebar**, no router library needed
(state-based tab switching in `App.tsx`):

| View | Purpose |
|---|---|
| **Dashboard** (default) | status + quick controls + rule summary + **live traffic feed**. The centerpiece. |
| **Rules** | full rule list, one-click toggle, open editor for add/edit |
| **Settings** | target URL, proxy port, config path (+ first-run onboarding) |

Sidebar (≈200px): brand wordmark top, nav items with the active one indicated
by `--c-accent` left bar, and a bottom mini-status chip (`RUNNING :8080` /
`STOPPED`) so the proxy state is visible from every view.

---

## 4. Screen wireframes

Legend: `[>]` start, `[|]` stop, `[+]` add, `[i]` info, `(o)` enabled toggle,
`( )` disabled toggle.

### 4.1 Dashboard (centerpiece)

```
+--------------------------------------------------------------------------+
| cha0s;sim  (o/o) RUNNING  :8080 -> http://localhost:3000   [settings]   |
+---------------------+----------------------------------------------------+
| cha0s;sim           |  STATUS                                RULES       |
|  > Dashboard        |  +------------------------------------------------+
|    Rules            |  | target      http://localhost:3000  4 active     |
|    Settings         |  | port        :8080                 1 disabled   |
|                     |  | rules       4 active / 1 disabled  + add rule   |
|                     |  | [> START PROXY]  [| STOP PROXY]                |
|  RUNNING :8080      |  +------------------------------------------------+
+---------------------+  TRAFFIC  (live, auto-follows newest at bottom)    |
                          +----------------------------------------------+
                          | 14:02:03.1  GET   /api/users    -> 200  +285ms <|| latency>
                          | 14:02:04.0  POST  /api/orders   -> 503  (override @5%)
                          | 14:02:04.6  GET   /api/profile  -> DROPPED <| drop>
                          | 14:02:05.2  PUT   /api/cart     -> 200  +12ms  (clean)
                          +----------------------------------------------+
                          |  [pause]  [filter: all] [clear]   seen 1,241  |
                          +----------------------------------------------+
```

Traffic feed details:

- Monospace rows; columns: timestamp · method (color-coded text) · path ·
  result (status or `DROPPED`) · duration · chaos marker.
- Chaos marker is a chip using the severity color + short label
  (`latency +285ms`, `override 503 @5%`, `drop`, `mangle delete_keys`,
  `clean`). Rows with chaos get the left-edge 2px severity bar; clean rows are
  neutral.
- **No internal scrolling requirement to feel live**: cap the viewport at
  ~8–10 rows with the newest at the bottom and auto-scroll; oldest rows fall
  off. A `paused` toggle freezes collection for inspection.
- Backlog cap in the frontend ring buffer (e.g. 500 events) — the feed is a
  glanceable pager-compact view, not a log viewer.
- Empty state (nothing logged yet): "No traffic yet — start the proxy and hit a
  route." + inline hint to open Settings if the target isn't running.

### 4.2 Rules

```
+---------------------+----------------------------------------------------+
| cha0s;sim           |  RULES                                             |
|    Dashboard        |  +--------------------------------------------------+
|  > Rules            |  | name              route            chaos    rate  |
|    Settings         |  | (o) users latency  GET /api/users  loading  5%   |
|                     |  |                [latency][...]                    |
|                     |  | (o) orders outage ^/api/orders/.  override 100%  |
|                     |  |                [drop]                            |
|                     |  | ( ) example disab /api/legacy      drop     N/A   |
|                     |  | [+ ADD RULE]          [reload from config]       |
|  RUNNING :8080      |  +--------------------------------------------------+
+---------------------+  Click a row -> editor sheet slides in from right:  |
                     NOTE  method  headers                                   |
                     ->    error_rate  [enabled]                             |
                          latency: fixed_ms jitter_min jitter_max            |
                          status_override: code strip_body                   |
                          drop_connection                                    |
                          mangle: strategy target_keys                       |
                          [SAVE] [DELETE] [CANCEL]                           |
```

- Toggle = row-level `(o)` switch, one click.
- Rule-type chips use the same severity colors as the feed (latency amber,
  override red, drop violet, mangle cyan; disabled = dim `--c-ink-dim`).
- **Persistence strategy (important):** write `enabled` (and edited fields)
  back to the YAML and rely on the **existing fsnotify hot-reload** — rule
  edits then go live for free with zero new backend plumbing. An in-memory
  runtime override map (instant, non-persistent toggling) is listed as an
  optional follow-up, but **it does not exist in the backend today** and is
  therefore out of scope for the toggle v1. UI must surface reload errors
  (e.g. YAML write/validation failure) as a `--c-warn` banner.

### 4.3 Settings (first-run onboarding)

```
+---------------------+----------------------------------------------------+
| cha0s;sim           |  SETTINGS                                          |
|    Dashboard        |  First run? "Point cha0s;sim at your backend."      |
|    Rules            |  +--------------------------------------------------+
|  > Settings         |  TARGET URL        http://localhost:3000  [test]     |
|                     |  PROXY PORT        8080                              |
|                     |  CONFIG FILE       configs/chaos.yaml     [browse]    |
|                     |  [test connection -> ok]   [SAVE]                  |
|                     |  note: proxy must be stopped to change port          |
|  STOPPED            |  +--------------------------------------------------+
+---------------------+  About: cha0s;sim v0.1    docs    log file location |
```

- First-run detection: `store == nil` (no config file found at startup) or
  never-saved settings. First launch shows Settings with a one-line onboarding
  banner instead of a silent dashboard.
- `[test connection]` design: future binding that does an HTTP HEAD against the
  target through the proxy and reports reachable/unreachable — display-only,
  not enforced.
- Changing target/port while the proxy is running: block with an inline note
  (must stop first) to keep v1 stateless-simple.

---

## 5. Component structure

```
frontend/src/
  main.tsx                      unchanged (entry)
  App.tsx                       shell: sidebar tabs, selected-view switch
  types.ts                      TrafficEvent, RuleView, StatusSnapshot types
  hooks/
    useProxyStatus.ts           poll GetStatus()/GetRuleCount() on interval + after actions
    useRules.ts                 rules snapshot from binding; optimistic toggle + rollback
    useTrafficFeed.ts           subscribes to Wails events; ring buffer cap 500; paused flag
  components/
    layout/Sidebar.tsx, NavItem.tsx, WindowStatusChip.tsx
    common/Button.tsx, Switch.tsx, Badge.tsx (chaos-type chip), EmptyState.tsx, Banner.tsx
    dashboard/StatusPanel.tsx, StartStop.tsx, RuleSummary.tsx
    traffic/TrafficFeed.tsx, TrafficRow.tsx, FeedToolbar.tsx
    rules/RuleList.tsx, RuleRow.tsx, RuleEditorSheet.tsx, RuleForm.tsx
    settings/SettingsForm.tsx, ConnectionTest.tsx
    brand/Wordmark.tsx, CrosshairIcon.tsx
  styles/
    tokens.css                  CSS custom properties (palette, type, spacing)
    (if Tailwind chosen) tailwind.config maps tokens -> theme extension
```

State model: no state library. `useProxyStatus` polls (and refreshes
post-action); `useTrafficFeed` is a Wails `EventsOn` subscription
(`runtime.EventsOn("traffic", cb)`), ring-buffered.

---

## 6. Data contracts (backend gaps to close)

### 6.1 Existing bindings (already wired, TASK 25)

| Binding | Signature | Used by |
|---|---|---|
| `StartProxy()` | `string` (""=ok) | StartStop, StatusPanel |
| `StopProxy()` | `string` (""=ok) | StartStop, StatusPanel |
| `GetStatus()` | `string` (`Running on :8080 -> ...` \| `Stopped`) | StatusPanel, Sidebar chip |
| `GetRuleCount()` | `int` | StatusPanel, RuleSummary |

### 6.2 New bindings this design requires (mapped — not yet built)

| Binding | Signature | Maps to | Purpose |
|---|---|---|---|
| `GetRules()` | `[]RuleView` | `app.store.Current().Rules` (`config.Rule` → JSON view) | Rules list (flattened view of `config.Rule`) |
| `SaveSettings(target,port,configPath)` | `string` (""=ok) | new `App.appSetting`-style state + `config.NewStore` restart | Settings; restarts store; blocks while proxy running |
| `GetSettings()` | `SettingsView` | hardcoded defaults (`defaultTargetURL`, ...) in `app.go` | prefills Settings + first-run detection |
| `TestConnection(url)` | `string` | HTTP HEAD via `net/http` against target | Settings `[test]` |

Frontend-facing type contracts (JSON-serialized by Wails):

```go
type RuleView struct {                // derived from config.Rule in app.go
    Name        string
    Path        string
    PathRegex   string
    Methods     []string
    Headers     map[string]string
    ErrorRate   float64
    Enabled     bool
    Effects     []string             // latency | override | drop | mangle
    Latency     string               // human-readable, e.g. "+250ms (±0..50ms)"
    StatusCode  int
    StripBody   bool
    MangleStrategy string
}

type SettingsView struct {
    TargetURL  string
    ProxyPort  int
    ConfigPath string
    FirstRun   bool                  // true when store == nil (no config yet)
}
```

Data flow:
- **GetRules / GetRuleCount** → read-only snapshot of `store.Current()` (never
  mutated). Rules list renders from `GetRules`; count/stat chips from
  `GetRuleCount`.
- **SaveSettings** → validates inputs in Go (URL scheme http/https, port range),
  refuses while `running == true` (short-circuit string error), then
  restarts the store via `config.NewStore(configPath)` so hot-reload resumes on
  the new file.
- **TestConnection** → blocking HEAD with a short timeout; returns a
  human-readable `ok` / failure string. Display-only, never enforced.

Rule toggle/editor writes go through the YAML file (existing hot-reload) — no
new runtime mutation endpoint in v1.

### 6.3 Live traffic event — implemented (TASK 26)

Previously the proxy knew **timing** (`logger.Middleware`: method, path, status,
elapsed) but **which injectors resolved** was discarded and nothing was
emitted. That seam now exists:

- `internal/proxy/events.go`: `EventSink interface { Emit(TrafficEvent) }`,
  `TrafficEvent`, `ChaosEffect`, plus the `withEventSink` middleware and
  `effectsForRule` mapping. A nil sink short-circuits to the plain handler, so
  the headless CLI incurs zero overhead.
- `NewServerInstance(..., sinks ...EventSink)` — variadic (backward compatible:
  the CLI and prior app call sites compile unchanged; CLI stays sink-less).
- `internal/chaos/orchestrator.go`: `FiredRule` + `ResolveFired(default)` keep
  rule attribution; the proxy `ChaosMiddleware` now derives effects
  from fired rules and shares them with the emitter via the request context.
- Desktop wiring (`app.go`): an **unexported** `trafficEventSink` forwards
  events with `runtime.EventsEmit(ctx, "traffic", evt)` — unexported on
  purpose so `Emit` does NOT appear as a frontend-callable binding (verified:
  generated `App.js` still lists only the four control bindings). Frontend
  subscribes with `runtime.EventsOn("traffic", cb)`.

Event contract:

```go
type TrafficEvent struct {
    Ts         time.Time     `json:"ts"`          // request start time
    Method     string        `json:"method"`
    Path       string        `json:"path"`
    Host       string        `json:"host"`
    Status     int           `json:"status"`      // 0 when connection dropped pre-response
    DurationMs int64         `json:"duration_ms"`
    Effects    []ChaosEffect `json:"effects"`     // empty = clean passthrough
}
type ChaosEffect struct {
    Kind   string `json:"kind"`   // latency|override|drop|mangle (mirrors effectsForRule gating)
    Detail string `json:"detail"` // "+250ms" / "=> 503 (body stripped)" / "connection dropped" ...
    Rule   string `json:"rule"`   // firing rule name
}
```

Behavior notes:
- **Truthfulness**: effects mirror `chaos.BuildInjectors` gating exactly; a
  rule that matches but fails the `error_rate` gate contributes nothing.
- **Drops**: a dropped connection writes no header → recorder keeps status 0,
  so `Status: 0` is the explicit "connection severed" signal (the console
  logger still prints `-> 200` for its own recorder; the event is the source of
  truth for the feed).
- **WebSockets**: upgrade requests skip emission (nothing is proxied through
  the chaos layer for them).
- **Duration** includes request-phase latency injection (measured around the
  full chain), matching what the user actually felt.

### 6.4 Redundancy note

The CLI admin server (`internal/admin`) already serves a static
"cha0s;sim Admin Dashboard" page on `:8090`. The desktop app supersedes that
surface. **Open question:** retire/redirect the admin server now that the
desktop feed exists, or keep as a headless/CI-readable health surface
(`/healthz` only)? Recommended: keep `/healthz`, drop the HTML page.

---

## 7. Design tokens (start here when implementing)

```css
/* styles/tokens.css — single source of truth; Tailwind v4 maps these 1:1 */
:root {
  /* surface */
  --c-bg-950:#0B1220; --c-bg-900:#1B2636; --c-bg-850:#223049;
  --c-bg-800:#2A3A59; --c-line:#32446A;
  /* ink */
  --c-ink-max:#EDF2FA; --c-ink:#B6C2D9; --c-ink-dim:#7C8BA6;
  /* accent + chaos */
  --c-accent:#7DF9FF; --c-ok:#34D399; --c-latency:#F59E0B;
  --c-override:#EF4444; --c-drop:#C084FC; --c-mangle:#22D3EE; --c-warn:#FDE047;
  /* type */
  --font-ui: system-ui, -apple-system, "Segoe UI", Ubuntu, Cantarell, sans-serif;
  --font-mono:"JetBrains Mono","Cascadia Code",Consolas,ui-monospace,monospace;
  /* structure */
  --radius-sm:6px; --radius-md:8px; --radius-lg:10px;
  --pad:16px; --gap:12px; --gap-sm:8px;
  --sidebar-w:200px;
}
```

Optional Tailwind v4 theme extension (`@theme`) mirrors the same names so
utility classes like `bg-[--c-bg-900]` (or mapped `bg-surface`) compose with
the tokens. **Tailwind is not yet a dependency** — tokens-as-CSS-variables
means vanilla CSS ships first with zero install risk.

---

## 8. Implementation phases (suggested order)

1. **Phase A — Identity tokens**: `tokens.css`, update window
   `BackgroundColour` to `--c-bg-900`, install font-face only if bundling mono
   (else system stack). No behavior change.
2. **Phase B — Shell/nav**: Sidebar + tab switch + window status chip.
3. **Phase C — Dashboard read-only**: StatusPanel + StartStop + RuleSummary
   pulling existing bindings (no backend change).
4. **Phase D — Traffic feed**: the 6.3 backend seam (observer + emit + EventsEmit)
   and the frontend feed. Highest-value, medium risk → do early, not last.
5. **Phase E — Rules**: list + YAML write-back editor + toggle (hot-reload
   already applies).
6. **Phase F — Settings**: bindings 6.2 + first-run onboarding + test connection.
7. **Phase G — Brand pass**: reticle-zero wordmark + app icon asset.

Each phase ends with `wails build` + the manual smoke checklist (start proxy →
watch feed → stop).

---

## 9. Open questions for the human

1. Keep or retire the CLI admin dashboard HTML at `:8090` given the desktop
   feed (recommend: keep only `/healthz`)?
2. Tailwind as the styling mechanism, or vanilla CSS + the tokens above?
   (Recommend: tokens first; Tailwind optional wrapper.)
3. Should rule edits persist to YAML (recommended) or instead need the
   not-yet-built in-memory override map first?
4. Bundled JetBrains Mono vs system mono stack (bundled = consistent branding,
   +~200KB asset)?