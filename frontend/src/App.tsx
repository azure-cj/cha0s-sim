import {ChangeEvent, useCallback, useEffect, useMemo, useRef, useState, type KeyboardEvent} from 'react';
import {
    Activity,
    AlertTriangle,
    ArrowRight,
    Boxes,
    Check,
    ChevronDown,
    Crosshair,
    Dna,
    Flame,
    Shield,
    ShieldAlert,
    SlidersHorizontal,
    TrendingUp,
    Turtle,
    Unplug,
    Zap,
    type LucideIcon,
} from 'lucide-react';
import {ClipboardSetText, EventsOn} from "../wailsjs/runtime/runtime";
import {
    CreateRule,
    GetAllSessionStatuses,
    GetDiscoveredEndpoints,
    GetRules,
    GetSessionStatus,
    GetSettings,
    GetStressStatus,
    SaveSettings,
    StartSession,
    StartStressTest,
    StopSession,
    StopStressTest,
    ToggleRule,
} from "../wailsjs/go/main/App";
import {config, discovery, main} from '../wailsjs/go/models';
import bootBackground from './assets/images/boot-background.jpg';
import './App.css';

type Severity = 'info' | 'warning' | 'critical';

interface TrafficEventPayload {
    ts: string;
    method: string;
    path: string;
    host: string;
    status: number;
    duration_ms: number;
    sessionName: string;
    effects?: {kind: string; detail: string; rule: string}[];
}

interface FindingPayload {
    FindingCategory: string;
    Detail: string;
    FindingSeverity: string;
    Location: string;
    remediation: string; // 'how to fix' guidance emitted by the backend (json:"remediation")
    sessionName: string;
}

interface StressUpdatePayload {
    totalRequests: number;
    totalErrors: number;
    errorRate: number;
    rps: number;
    p50Ms: number;
    p95Ms: number;
    p99Ms: number;
    final: boolean;
    errorCategories?: Record<string, number>; // absent when no errors recorded (omitempty on the wire)
}

interface LogEntry {
    id: number;
    time: string;
    tsMs: number; // wall-clock ms when the entry arrived (drives suggestion expiry)
    severity: Severity;
    summary: string;
    sessionName: string; // 'chaos', 'security', or '' for CLI-originated events
    remediation?: string; // 'how to fix' guidance, only set on security-finding entries that have one
    event?: TrafficEventPayload; // present only for traffic entries (not security findings)
    finding?: FindingPayload;    // present only for security-finding entries
}

// Suggestion badges on clean traffic events let beginners inject chaos onto an
// exact path/method in one click. Each action tracks its own lifecycle so a
// created rule can show "Added" feedback and a failure can surface its error.
interface SuggestionState {
    latency: {status: 'idle' | 'creating' | 'added' | 'error'; message?: string};
    error: {status: 'idle' | 'creating' | 'added' | 'error'; message?: string};
}

type SuggestionAction = 'latency' | 'error';

type View = 'dashboard' | 'chaos' | 'stress' | 'security' | 'settings' | 'help';

// Fixed session ports/names. MUST stay in sync with the backend's
// sessionPorts map in app.go (sessionChaos -> 8081, sessionSecurity -> 8082).
const CHAOS_SESSION_PORT = 8081;
const CHAOS_SESSION_NAME = 'chaos';
const SECURITY_SESSION_PORT = 8082;
const SECURITY_SESSION_NAME = 'security';

// The canonical set of named sessions shown in the Dashboard overview and the
// persistent sidebar indicator. Stress Test is intentionally excluded: it is
// not part of the sessionName/port system (no fixed port, its own lifecycle)
// and has its own dedicated tab, so folding it in here would add confusion.
const SESSION_OVERVIEW = [
    {name: CHAOS_SESSION_NAME, label: 'Chaos', port: CHAOS_SESSION_PORT, view: 'chaos' as View},
    {name: SECURITY_SESSION_NAME, label: 'Security', port: SECURITY_SESSION_PORT, view: 'security' as View},
];

const BOOT_WORDMARK = 'cha0s;sim';

function useTypewriter(text: string, speedMs: number): string {
    const [revealed, setRevealed] = useState('');
    useEffect(() => {
        setRevealed('');
        if (text.length === 0) {
            return;
        }
        let i = 0;
        const id = window.setInterval(() => {
            i += 1;
            setRevealed(text.slice(0, i));
            if (i >= text.length) {
                window.clearInterval(id);
            }
        }, speedMs);
        return () => window.clearInterval(id);
    }, [text, speedMs]);
    return revealed;
}

function trafficSeverity(status: number): Severity {
    if (status === 0 || status >= 500) return 'critical';
    if (status >= 400) return 'warning';
    return 'info';
}

function normalizeSeverity(s: string): Severity {
    if (s === 'critical') return 'critical';
    if (s === 'warning') return 'warning';
    return 'info';
}

function summarizeTraffic(evt: TrafficEventPayload): string {
    const status = evt.status === 0 ? 'DROP' : evt.status;
    const base = `${evt.method} ${evt.path} -> ${status} (${evt.duration_ms}ms)`;
    if (!evt.effects || evt.effects.length === 0) {
        return base;
    }
    const effects = evt.effects.map(e => {
        const detail = e.detail ? `:${e.detail}` : '';
        return `${e.kind}${detail}`;
    }).join(', ');
    return `${base} [${effects}]`;
}

// generateCurl reconstructs a MINIMAL curl command (method + URL) from a
// recorded traffic event, pointed at the configured upstream target URL.
// Traffic events do not capture the original request headers, body, or query
// string, so a faithful reproduction is not possible — the copy action adds an
// explicit disclosure comment above the command rather than claiming full
// request reconstruction.
const CURL_DISCLOSURE = '# Method + path only — original request headers, body, and query string are not captured by traffic events';
function generateCurl(event: TrafficEventPayload, targetURL: string): string {
    const method = (event.method || 'GET').toUpperCase();
    const base = (targetURL || '').replace(/\/+$/, '');
    const path = event.path || '/';
    const url = `${base}${path}`;
    return method === 'GET'
        ? `curl "${url}"`
        : `curl -X ${method} "${url}"`;
}

// ---------------------------------------------------------------------------
// Resilience & Security Scorecard
// ---------------------------------------------------------------------------

// ScorecardStats is the derived snapshot the Dashboard's "Damage Control
// Certificate" modal renders and exports. It is computed from the live log
// entries plus the most recent stress-test snapshot (when one exists).
interface ScorecardStats {
    generatedAt: string;
    traffic: {
        total: number;   // traffic events seen
        mutated: number; // events that had at least one chaos effect fire
        hitRate: number; // mutated / total (0..1), 0 when total is 0
    };
    security: {
        missingHeaders: number; // missing_header + weak_header findings
        leakedSecrets: number;  // leaked_secret findings
        findingCount: number;   // total security findings
    };
    performance: {
        present: boolean; // false when no stress data has been received
        p50Ms: number;
        p95Ms: number;
        p99Ms: number;
        rps: number;
        errorRate: number;
    };
}

// An entry belongs to the "chaos" traffic scope if it came from the chaos
// session, or is still un-tagged ('' — CLI-originated traffic that predates
// session tagging). Security-health stats are scoped strictly to entries tagged
// 'security', since findings only ever originate from the security session.
function isChaosTraffic(sessionName: string): boolean {
    return sessionName === CHAOS_SESSION_NAME || sessionName === '';
}

// computeScorecard derives the certificate numbers from the combined log,
// filtering by sessionName so the chaos hit rate and security-health counts are
// scoped to their own session and are not diluted by the other session's
// traffic. Only entries carrying a TrafficEventPayload count toward traffic
// stats; only entries carrying a FindingPayload count toward security stats.
function computeScorecard(entries: LogEntry[], stress: StressUpdatePayload | null): ScorecardStats {
    let trafficTotal = 0;
    let trafficMutated = 0;
    let missingHeaders = 0;
    let leakedSecrets = 0;
    let findingCount = 0;

    for (const entry of entries) {
        if (entry.event && isChaosTraffic(entry.sessionName)) {
            trafficTotal += 1;
            if (entry.event.effects && entry.event.effects.length > 0) {
                trafficMutated += 1;
            }
        }
        if (entry.finding && entry.sessionName === SECURITY_SESSION_NAME) {
            findingCount += 1;
            if (entry.finding.FindingCategory === 'leaked_secret') {
                leakedSecrets += 1;
            } else if (
                entry.finding.FindingCategory === 'missing_header' ||
                entry.finding.FindingCategory === 'weak_header'
            ) {
                missingHeaders += 1;
            }
        }
    }

    return {
        generatedAt: new Date().toLocaleString(),
        traffic: {
            total: trafficTotal,
            mutated: trafficMutated,
            hitRate: trafficTotal === 0 ? 0 : trafficMutated / trafficTotal,
        },
        security: {
            missingHeaders,
            leakedSecrets,
            findingCount,
        },
        performance: {
            present: stress !== null,
            p50Ms: stress?.p50Ms ?? 0,
            p95Ms: stress?.p95Ms ?? 0,
            p99Ms: stress?.p99Ms ?? 0,
            rps: stress?.rps ?? 0,
            errorRate: stress?.errorRate ?? 0,
        },
    };
}

// buildMarkdownReport renders the certificate as a clean, screenshot-friendly
// Markdown document that developers can paste into PR descriptions or share.
// It carries the same honesty as the on-screen modal: the 200-event scope is
// always disclosed and the stress-test status (live vs. last completed) is
// labelled. Per-session scoping of the numbers means no mixed-session caveat is
// needed.
type ReportContext = {
    stressRunning: boolean;      // a stress test is currently streaming updates
    stressCompletedAt: string | null; // time when the last completed run sent its final snapshot
};

const SCORECARD_SCOPE_NOTE = 'Based on the most recent 200 events shown in the live feed — not your entire session.';
const SCORECARD_PERF_LIVE = 'Live — test in progress';
const SCORECARD_PERF_COMPLETED = (t: string) => `Latest stress test (completed at ${t})`;

function buildMarkdownReport(stats: ScorecardStats, opts: ReportContext): string {
    const pct = (v: number) => `${Math.round(v * 100)}%`;
    const perfStatus = opts.stressRunning
        ? 'Stress Test — live'
        : opts.stressCompletedAt
        ? `Stress Test — completed at ${opts.stressCompletedAt}`
        : 'Stress Test';
    const lines = [
        '# Damage Control Certificate — cha0s;sim',
        '',
        `Generated: ${stats.generatedAt}`,
        '',
        '## Traffic Health',
        `- Total requests processed: ${stats.traffic.total}`,
        `- Total mutated requests: ${stats.traffic.mutated}`,
        `- Chaos hit rate: ${pct(stats.traffic.hitRate)}`,
        '',
        '## Security Health',
        `- Missing / weak headers: ${stats.security.missingHeaders}`,
        `- Leaked secrets: ${stats.security.leakedSecrets}`,
        `- Total findings: ${stats.security.findingCount}`,
        '',
    ];
    if (stats.performance.present) {
        lines.push(
            `## Performance (${perfStatus})`,
            `- p50: ${stats.performance.p50Ms} ms`,
            `- p95: ${stats.performance.p95Ms} ms`,
            `- p99: ${stats.performance.p99Ms} ms`,
            `- RPS: ${Math.round(stats.performance.rps)}`,
            `- Error rate: ${pct(stats.performance.errorRate)}`,
            '',
        );
    } else {
        lines.push('## Performance (Stress Test)', '- No stress data recorded.', '');
    }
    lines.push(
        '## Notes',
        `- ${SCORECARD_SCOPE_NOTE}`,
    );
    lines.push('', '---', '', '_Generated by cha0s;sim_');
    return lines.join('\n');
}

// Plain-language phrasing helpers for the Chaos Engine view. These translate
// rule config (path/regex/methods/error_rate) into sentences a non-technical
// user can parse at a glance; the raw technical details stay visible as
// smaller, muted secondary text so power users retain the exact values.

// chaosMethodsPhrase folds the restricted methods into a natural reading
// ("GET and POST", "GET, POST, and PUT"); null means "all request types".
function chaosMethodsPhrase(methods?: string[]): string | null {
    if (!methods || methods.length === 0) return null;
    if (methods.length === 1) return methods[0];
    if (methods.length === 2) return `${methods[0]} and ${methods[1]}`;
    return `${methods.slice(0, -1).join(', ')}, and ${methods[methods.length - 1]}`;
}

// chaosSummary is the lead sentence for a rule. It carries the plain-language
// "what does this rule target" in prominent position; the raw regex stays
// visible but de-emphasized (matched against .chaos-muted).
function chaosSummary(r: main.RuleView) {
    const methods = chaosMethodsPhrase(r.methods);
    if (r.pathRegex) {
        // Regex rules keep the lead sentence method-less (per spec template);
        // the raw method list is a muted secondary detail below.
        return (
            <>
                Affects requests matching a pattern{' '}
                <span className="chaos-muted">(regex: {r.pathRegex})</span>
            </>
        );
    }
    const path = r.path && r.path.length > 0 ? r.path : 'any endpoint';
    if (methods) return <>Affects {methods} requests to {path}</>;
    return <>Affects requests to {path}</>;
}

// chaosFrequencyText renders error_rate as a friendly frequency instead of a
// bare percentage. Exact clean fractions become "1 in N"; values that don't
// round cleanly fall back to a friendly "~X% of the time". error_rate 0 means
// the rule's effects NEVER fire (matcher.ShouldFire gates on it) and 1.0 means
// always, so both get a plain word rather than a fraction.
function chaosFrequencyText(errorRate: number): string {
    if (errorRate <= 0) return 'Never triggers';
    if (errorRate >= 1) return 'Triggers every time';
    const n = Math.round(1 / errorRate);
    if (n <= 1) return 'Triggers every time';
    // Accept "1 in N" only when the rounded fraction is within ~5% of the
    // configured rate; otherwise use a friendly percentage.
    if (Math.abs(1 / n - errorRate) / errorRate <= 0.05) {
        return `Triggers on about 1 in ${n} requests`;
    }
    return `Triggers about ${Math.round(errorRate * 100)}% of the time`;
}

function chaosErrorRateLabel(r: main.RuleView): string {
    return `${Math.round((r.errorRate || 0) * 100)}%`;
}

// chaosMethodsDetail is the raw "Methods: GET, POST" secondary text (or a plain
// "Applies to all request types" when unrestricted).
function chaosMethodsDetail(r: main.RuleView): string {
    if (!r.methods || r.methods.length === 0) return 'Applies to all request types';
    return `Methods: ${r.methods.join(', ')}`;
}

// ---------- per-rule live activity ----------

// Per-rule activity is derived from the same in-memory entry buffer the
// Dashboard and Scorecard already read, scoped to chaos traffic by
// isChaosTraffic. That keeps these numbers from ever disagreeing with the log
// on screen. The buffer holds the most recent 200 entries and is not
// persisted, so the stats describe traffic currently in view, not all history.
interface RuleActivity {
    count: number; // traffic events in which this rule fired
    last: TrafficEventPayload | null; // most recent such event
    recent: LogEntry[]; // newest first, trimmed for display
}

const RULE_ACTIVITY_LIMIT = 5;

// The proxy stamps every ChaosEffect with the name of the rule that produced
// it, and one event can carry effects from several rules at once. Membership
// (not effect count) is the attribution test, so a rule that contributed three
// effects to one request still counts as one fire.
function eventFiredRule(evt: TrafficEventPayload, ruleName: string): boolean {
    return !!evt.effects?.some(e => e.rule === ruleName);
}

function ruleActivity(entries: LogEntry[], ruleName: string): RuleActivity {
    const matches = entries.filter(
        e => !!e.event && isChaosTraffic(e.sessionName) && eventFiredRule(e.event, ruleName)
    );
    return {
        count: matches.length,
        last: matches.length > 0 ? matches[0].event! : null,
        recent: matches.slice(0, RULE_ACTIVITY_LIMIT),
    };
}

// relativeTime is fed App's 1s clock so "12s ago" keeps counting down instead
// of freezing at whatever the value was when the event arrived.
function relativeTime(tsMs: number, nowMs: number): string {
    const seconds = Math.max(0, Math.round((nowMs - tsMs) / 1000));
    if (seconds < 5) return 'just now';
    if (seconds < 60) return `${seconds}s ago`;
    const minutes = Math.round(seconds / 60);
    if (minutes < 60) return `${minutes}m ago`;
    return `${Math.round(minutes / 60)}h ago`;
}

// Reports only what the events actually contain: how many times the rule fired,
// how recently, and the status/duration of the newest one. It deliberately
// stops short of judging whether chaos succeeded — that verdict belongs to the
// Security session's findings, not to a status code.
function ruleActivitySummary(activity: RuleActivity, nowMs: number): string {
    const {count, last} = activity;
    if (count === 0 || !last) return 'Not triggered yet in this session';
    const firedAt = new Date(last.ts).getTime();
    const ago = Number.isFinite(firedAt) ? `, ${relativeTime(firedAt, nowMs)}` : '';
    const status = last.status === 0 ? 'DROP' : last.status;
    return `Fired ${count} ${count === 1 ? 'time' : 'times'}${ago} \u00b7 last: ${status} (${last.duration_ms}ms)`;
}

// The disclosure reuses the rotating ChevronDown from the log's "HOW TO FIX"
// rows so expansion reads the same everywhere in the app.
function RuleActivityDetails({activity}: {activity: RuleActivity}) {
    const [open, setOpen] = useState(false);
    if (activity.count === 0) return null;
    return (
        <div className="chaos-row-activity">
            <button
                type="button"
                className="chaos-activity-toggle"
                onClick={() => setOpen(v => !v)}
                aria-expanded={open}
            >
                <span className={`log-chevron${open ? ' log-chevron--open' : ''}`} aria-hidden="true">
                    <ChevronDown size={14} strokeWidth={2} />
                </span>
                Recent activity
                {activity.count > activity.recent.length && ` (last ${activity.recent.length})`}
            </button>
            {open && (
                <ul className="chaos-activity-list">
                    {activity.recent.map(entry => {
                        const evt = entry.event as TrafficEventPayload;
                        const status = evt.status === 0 ? 'DROP' : evt.status;
                        return (
                            <li className="chaos-activity-item" key={entry.id}>
                                <span className="chaos-activity-time">{entry.time}</span>
                                <span className="chaos-activity-request">{evt.method} {evt.path}</span>
                                <span className="chaos-activity-result">{status} ({evt.duration_ms}ms)</span>
                            </li>
                        );
                    })}
                </ul>
            )}
        </div>
    );
}

interface ChaosTag {
    kind: string;
    label: string; // icon + short plain-language label shown in the pill
    title: string; // HTML title tooltip with a one-line plain-language description
}

// Chaos-effect tags share the accent-primary pill styling; each effect is
// differentiated by its lucide glyph, so the pills stay a uniform accent color
// via currentColor instead of mixing severities in a compact row.
const CHAOS_EFFECT_ICONS: Record<string, LucideIcon> = {
    latency: Turtle,
    status: AlertTriangle,
    drop: Unplug,
    mangle: Dna,
    fuzz: Crosshair,
};

function ChaosTagPill({tag}: {tag: ChaosTag}) {
    const Icon = CHAOS_EFFECT_ICONS[tag.kind];
    return (
        <span className="chaos-tag" title={tag.title}>
            {Icon && <Icon size={12} strokeWidth={2.5} aria-hidden="true" />}
            {tag.label}
        </span>
    );
}

function chaosTags(r: main.RuleView): ChaosTag[] {
    const tags: ChaosTag[] = [];
    if (r.hasLatency) tags.push({kind: 'latency', label: 'Slows it down', title: 'Delays the response to simulate a slow network'});
    if (r.hasStatusOverride) tags.push({kind: 'status', label: 'Fakes an error', title: 'Returns a fake error status like 500 or 503'});
    if (r.hasDropConnection) tags.push({kind: 'drop', label: 'Cuts the connection', title: 'Simulates the connection suddenly dropping, like lost WiFi'});
    if (r.hasMangle) tags.push({kind: 'mangle', label: 'Corrupts the data', title: 'Deletes or corrupts fields in the response'});
    if (r.hasFuzz) tags.push({kind: 'fuzz', label: 'Injects bad input', title: 'Sends malicious test payloads like SQL injection or XSS strings'});
    return tags;
}

function SettingsForm() {
    const [targetURL, setTargetURL] = useState('');
    const [configPath, setConfigPath] = useState('');
    const [firstRun, setFirstRun] = useState(false);
    const [error, setError] = useState('');
    const [success, setSuccess] = useState(false);
    const successTimer = useRef<number | null>(null);

    const applyFromServer = (s: main.SettingsView) => {
        setTargetURL(s.targetURL);
        setConfigPath(s.configPath);
        setFirstRun(s.firstRun);
    };

    useEffect(() => {
        let cancelled = false;
        GetSettings().then(s => {
            if (!cancelled) applyFromServer(s);
        });
        return () => {
            cancelled = true;
            if (successTimer.current !== null) window.clearTimeout(successTimer.current);
        };
    }, []);

    const save = async () => {
        setError('');
        setSuccess(false);
        const err = await SaveSettings(targetURL, configPath);
        if (err) {
            setError(err);
            return;
        }
        setSuccess(true);
        if (successTimer.current !== null) window.clearTimeout(successTimer.current);
        successTimer.current = window.setTimeout(() => setSuccess(false), 2500);
        GetSettings().then(applyFromServer);
    };

    return (
        <div className="settings">
            <div className="settings-title">PROXY SETTINGS</div>

            {firstRun && (
                <div className="settings-note">
                    No config file loaded yet — the proxy will run in passthrough mode until a valid chaos.yaml is
                    set.
                </div>
            )}

            <form className="settings-form" onSubmit={e => { e.preventDefault(); save(); }}>
                <div className="settings-field">
                    <label htmlFor="settings-target">Target URL</label>
                    <input
                        className="settings-input"
                        id="settings-target"
                        type="text"
                        placeholder="http://localhost:3000"
                        value={targetURL}
                        onChange={e => setTargetURL(e.target.value)}
                    />
                </div>

                <div className="settings-field">
                    <label htmlFor="settings-config">Config Path</label>
                    <input
                        className="settings-input"
                        id="settings-config"
                        type="text"
                        placeholder="chaos.yaml"
                        value={configPath}
                        onChange={e => setConfigPath(e.target.value)}
                    />
                </div>

                <div className="settings-actions">
                    <button className="btn" type="submit">
                        Save Settings
                    </button>
                    {error && <span className="settings-error">{error}</span>}
                    {success && <span className="settings-success">Settings saved</span>}
                </div>
            </form>
        </div>
    );
}

function ChaosEngine({entries, now}: {entries: LogEntry[]; now: number}) {
    const [rules, setRules] = useState<main.RuleView[]>([]);
    const [errors, setErrors] = useState<Record<string, string>>({});
    const [warnings, setWarnings] = useState<Record<string, string>>({});
    const [pending, setPending] = useState<string | null>(null);
    const warningTimers = useRef<Record<string, number>>({});

    const [wizardOpen, setWizardOpen] = useState(false);
    const [notice, setNotice] = useState<{text: string; kind: 'success' | 'warning'} | null>(null);
    const noticeTimer = useRef<number | null>(null);

    const [running, setRunning] = useState(false);
    const [busy, setBusy] = useState(false);
    const [sessionError, setSessionError] = useState('');

    // Poll this session's running state, mirroring the Security view.
    useEffect(() => {
        let cancelled = false;
        const poll = () => {
            GetSessionStatus(CHAOS_SESSION_NAME).then(s => {
                if (!cancelled) setRunning(s);
            });
        };
        poll();
        const id = window.setInterval(poll, 1000);
        return () => {
            cancelled = true;
            window.clearInterval(id);
        };
    }, []);

    const startSession = () => {
        setBusy(true);
        setSessionError('');
        StartSession(CHAOS_SESSION_NAME).then(err => {
            setBusy(false);
            if (err) {
                setSessionError(err);
                return;
            }
            GetSessionStatus(CHAOS_SESSION_NAME).then(setRunning);
        });
    };

    const stopSession = () => {
        setBusy(true);
        setSessionError('');
        StopSession(CHAOS_SESSION_NAME).then(err => {
            setBusy(false);
            if (err) {
                setSessionError(err);
                return;
            }
            GetSessionStatus(CHAOS_SESSION_NAME).then(setRunning);
        });
    };


    const loadRules = useCallback(() => {
        GetRules().then(setRules);
    }, []);

    useEffect(() => {
        loadRules();
        return () => {
            Object.values(warningTimers.current).forEach(id => window.clearTimeout(id));
            warningTimers.current = {};
            if (noticeTimer.current !== null) window.clearTimeout(noticeTimer.current);
        };
    }, [loadRules]);

    const showNotice = (kind: 'success' | 'warning', text: string) => {
        if (noticeTimer.current !== null) window.clearTimeout(noticeTimer.current);
        setNotice({kind, text});
        noticeTimer.current = window.setTimeout(() => setNotice(null), 5000);
    };

    // Called by the wizard when the create call finishes. Every outcome closes
    // the wizard and refreshes the list (a warning still means the rule exists
    // in-memory and serves live traffic); only a warning or success shows a
    // banner — a hard failure keeps the wizard open for correction instead.
    const handleCreated = (payload: {kind: 'success'; name: string} | {kind: 'warning'; message: string}) => {
        setWizardOpen(false);
        loadRules();
        if (payload.kind === 'success') {
            showNotice('success', `Rule "${payload.name}" created — it's live now and saved to your config file.`);
        } else {
            showNotice('warning', payload.message);
        }
    };

    const setRuleWarning = (name: string, text: string) => {
        setWarnings(prev => ({...prev, [name]: text}));
        if (warningTimers.current[name]) window.clearTimeout(warningTimers.current[name]);
        warningTimers.current[name] = window.setTimeout(() => {
            setWarnings(prev => {
                if (!prev[name]) return prev;
                const next = {...prev};
                delete next[name];
                return next;
            });
            delete warningTimers.current[name];
        }, 3500);
    };

    const toggle = async (rule: main.RuleView, newEnabled: boolean) => {
        if (pending !== null) return;
        setPending(rule.name);
        setErrors(prev => {
            if (!prev[rule.name]) return prev;
            const next = {...prev};
            delete next[rule.name];
            return next;
        });
        setWarnings(prev => {
            if (!prev[rule.name]) return prev;
            const next = {...prev};
            delete next[rule.name];
            return next;
        });
        setRules(prev => prev.map(r => (r.name === rule.name ? {...r, enabled: newEnabled} : r)));

        const result = await ToggleRule(rule.name, newEnabled);
        setPending(null);

        if (result === '') {
            loadRules();
            return;
        }
        if (result.startsWith('warning:')) {
            // Toggled in-memory but could not be persisted to disk: keep the
            // flipped state (the toggle DID take effect for live traffic) and
            // show a soft amber warning that fades after a few seconds.
            setRuleWarning(rule.name, result.slice('warning:'.length).trim());
            loadRules();
            return;
        }
        // Hard failure (rule not found, no config): revert the optimistic flip
        // and show a persistent red error near this rule.
        setRules(prev => prev.map(r => (r.name === rule.name ? {...r, enabled: !newEnabled} : r)));
        setErrors(prev => ({...prev, [rule.name]: result}));
    };

    const activeCount = rules.filter(r => r.enabled).length;
    const ruleWord = rules.length === 1 ? 'rule' : 'rules';
    const activeWord = activeCount === 1 ? 'is' : 'are';

    return (
        <div className="chaos">
            <div className="chaos-title">CHAOS RULES</div>
            <div className="security-controls">
                <div className="security-status">
                    <span className={`status-dot${running ? ' status-dot--running' : ''}`} />
                    <span className="security-status-text">{running ? 'Running' : 'Stopped'}</span>
                </div>
                <button className="btn btn--primary" onClick={startSession} disabled={busy || running}>
                    Start Session
                </button>
                <button className="btn btn--danger" onClick={stopSession} disabled={busy || !running}>
                    Stop Session
                </button>
                <span className="security-port">port {CHAOS_SESSION_PORT}</span>
                {sessionError && <span className="settings-error">{sessionError}</span>}
            </div>
            <div className="chaos-summary">
                You have {rules.length} {ruleWord} configured. {activeCount} {activeWord} currently active.
            </div>
            <div className="chaos-disclaimer">
                Turning a rule on/off here works right away, but won't be remembered if you edit your config file
                directly.
            </div>
            <div className="chaos-toolbar">
                <button className="btn chaos-create-btn" onClick={() => setWizardOpen(true)}>
                    + CREATE A NEW RULE
                </button>
            </div>
            {notice && (
                <div className={`wizard-notice wizard-notice--${notice.kind}`}>{notice.text}</div>
            )}
            <div className="chaos-list">
                {rules.length === 0 && (
                    <div className="chaos-empty">
                        No rules loaded. Add rules to your chaos.yaml and they'll appear here.
                    </div>
                )}
                {rules.map(r => {
                    const disabled = pending !== null;
                    const err = errors[r.name];
                    const warn = warnings[r.name];
                    const tags = chaosTags(r);
                    const activity = ruleActivity(entries, r.name);
                    return (
                        <div className="chaos-row" key={r.name}>
                            <div className="chaos-row-info">
                                <div className="chaos-row-name">{r.name}</div>
                                <div className="chaos-row-summary">{chaosSummary(r)}</div>
                                <div className="chaos-row-frequency">{chaosFrequencyText(r.errorRate || 0)}</div>
                                <div className="chaos-row-meta">
                                    {(!r.pathRegex && r.methods && r.methods.length > 0) ? null : (
                                        <span>{chaosMethodsDetail(r)}</span>
                                    )}
                                    <span>Error rate: {chaosErrorRateLabel(r)}</span>
                                </div>
                                {tags.length > 0 && (
                                    <div className="chaos-row-tags">
                                        {tags.map(t => (
                                            <ChaosTagPill key={t.kind} tag={t} />
                                        ))}
                                    </div>
                                )}
                                <div
                                    className={`chaos-row-activity-summary${activity.count === 0 ? ' chaos-row-activity-summary--idle' : ''}`}
                                >
                                    {ruleActivitySummary(activity, now)}
                                </div>
                                <RuleActivityDetails activity={activity} />
                                {err && <div className="chaos-row-error">{err}</div>}
                                {warn && <div className="chaos-row-warning">{warn}</div>}
                            </div>
                            <label className={`chaos-switch${disabled ? ' chaos-switch--pending' : ''}`}>
                                <input
                                    type="checkbox"
                                    checked={r.enabled}
                                    onChange={e => toggle(r, e.target.checked)}
                                    disabled={disabled}
                                    aria-label={`toggle rule ${r.name}`}
                                />
                                <span className="chaos-switch-slider" />
                            </label>
                        </div>
                    );
                })}
            </div>
            {wizardOpen && <RuleCreateWizard onClose={() => setWizardOpen(false)} onCreated={handleCreated} />}
        </div>
    );
}

// ---------- create-rule wizard ----------

type EffectId = 'slow' | 'error' | 'drop' | 'corrupt' | 'attack';
type AttackId = 'sqli' | 'xss' | 'path_traversal';

const EFFECT_CARDS: {id: EffectId; icon: LucideIcon; color: string; name: string; desc: string}[] = [
    {id: 'slow', icon: Turtle, color: 'var(--accent-primary)', name: 'Slow it down', desc: 'Delay the response to simulate a slow network'},
    {id: 'error', icon: AlertTriangle, color: 'var(--severity-warning)', name: 'Fake an error', desc: 'Return a fake error status instead of the real response'},
    {id: 'drop', icon: Unplug, color: 'var(--accent-primary)', name: 'Cut the connection', desc: 'Simulate the connection suddenly dropping'},
    {id: 'corrupt', icon: Dna, color: 'var(--log-security)', name: 'Corrupt the data', desc: 'Remove fields from the response'},
    {id: 'attack', icon: Crosshair, color: 'var(--severity-critical)', name: 'Test for security holes', desc: 'Send malicious test data to check input validation'},
];

const ERROR_CODE_CHOICES: {code: number; label: string}[] = [
    {code: 500, label: '500 Internal Server Error'},
    {code: 503, label: '503 Service Unavailable'},
    {code: 404, label: '404 Not Found'},
    {code: 429, label: '429 Too Many Requests'},
];

const ATTACK_CHOICES: {id: AttackId; label: string; desc: string}[] = [
    {id: 'sqli', label: 'SQL Injection', desc: 'Injects SQL that tries to break queries, like \' OR 1=1'},
    {id: 'xss', label: 'Cross-Site Scripting (XSS)', desc: 'Injects <script> tags to check for escaped output'},
    {id: 'path_traversal', label: 'Path Traversal', desc: 'Sends ../.. paths to check for unsafe file access'},
];

const METHOD_CHOICES = ['GET', 'POST', 'PUT', 'DELETE', 'PATCH'];

// The id is the EXACT frequency string the backend expects (CreateRule maps it
// to error_rate: always=1.0, often=0.5, sometimes=0.2, rarely=0.05).
const FREQUENCY_CARDS: {id: string; label: string; desc: string}[] = [
    {id: 'always', label: 'Every time', desc: 'Triggers on every matching request'},
    {id: 'often', label: 'About half the time', desc: 'Triggers on about half of matching requests'},
    {id: 'sometimes', label: 'Sometimes (about 1 in 5)', desc: 'Triggers on about 1 in 5 matching requests'},
    {id: 'rarely', label: 'Rarely (about 1 in 20)', desc: 'Triggers on about 1 in 20 matching requests'},
];

const FREQUENCY_RATES: Record<string, number> = {always: 1.0, often: 0.5, sometimes: 0.2, rarely: 0.05};

// EndpointCombo is the wizard Step A path field's autocomplete: the input IS the
// free-text entry (manual paths stay fully supported, no rigid dropdown-only
// control), with a suggestion list of endpoints actually observed on the session
// rendered directly beneath it. Standard autocomplete behavior — plain React
// state, no component library:
//   - typing filters the observed endpoints by substring match on the path
//   - the list appears on focus and stays open while the field is focused
//   - clicking (or keyboard arrow+enter) fills the path via onPick
//   - mousedown on an item is prevented so the input never blurs before the
//     click lands
function EndpointCombo({value, onChange, endpoints, onPick}: {
    value: string;
    onChange: (v: string) => void;
    endpoints: discovery.Endpoint[];
    onPick: (ep: discovery.Endpoint) => void;
}) {
    const [open, setOpen] = useState(false);
    const [active, setActive] = useState(-1);

    const query = value.trim().toLowerCase();
    // The backend already returns most-seen-first; the sort here just defends
    // the ordering regardless of where the list came from. Filtering preserves
    // that order, so the most-observed endpoints surface first as you type.
    const matches = useMemo(
        () => endpoints
            .filter(e => e.path.toLowerCase().includes(query))
            .sort((a, b) => b.seenCount - a.seenCount),
        [endpoints, query],
    );

    const choose = (ep: discovery.Endpoint) => {
        onPick(ep);
        setOpen(false);
        setActive(-1);
    };

    const onKeyDown = (e: KeyboardEvent<HTMLInputElement>) => {
        if (!open || matches.length === 0) {
            return;
        }
        if (e.key === 'ArrowDown') {
            e.preventDefault();
            setActive(i => (i + 1) % matches.length);
        } else if (e.key === 'ArrowUp') {
            e.preventDefault();
            setActive(i => (i <= 0 ? matches.length - 1 : i - 1));
        } else if (e.key === 'Enter') {
            if (active >= 0) {
                e.preventDefault();
                choose(matches[active]);
            }
        } else if (e.key === 'Escape') {
            setOpen(false);
        }
    };

    return (
        <div className="endpoint-combo">
            <input
                className="settings-input"
                id="wizard-path"
                type="text"
                placeholder="e.g. /api/checkout"
                value={value}
                onChange={e => {
                    onChange(e.target.value);
                    setOpen(true);
                    setActive(-1);
                }}
                onFocus={() => setOpen(true)}
                onBlur={() => setOpen(false)}
                onKeyDown={onKeyDown}
                autoComplete="off"
                role="combobox"
                aria-expanded={open && matches.length > 0}
                aria-autocomplete="list"
                aria-controls="wizard-path-suggestions"
            />
            {open && matches.length > 0 && (
                <ul className="endpoint-suggest" id="wizard-path-suggestions" role="listbox">
                    {matches.map((ep, i) => (
                        <li
                            key={`${ep.method} ${ep.path}`}
                            role="option"
                            aria-selected={i === active}
                            className={`endpoint-suggest-item${i === active ? ' endpoint-suggest-item--active' : ''}`}
                            onMouseDown={e => e.preventDefault()}
                            onMouseEnter={() => setActive(i)}
                            onClick={() => choose(ep)}
                        >
                            <span className="endpoint-suggest-method">{ep.method}</span>
                            <span className="endpoint-suggest-path">{ep.path}</span>
                            <span className="endpoint-suggest-count">
                                seen {ep.seenCount} {ep.seenCount === 1 ? 'time' : 'times'}
                            </span>
                        </li>
                    ))}
                </ul>
            )}
        </div>
    );
}

function RuleCreateWizard({onClose, onCreated}: {
    onClose: () => void;
    onCreated: (payload: {kind: 'success'; name: string} | {kind: 'warning'; message: string}) => void;
}) {
    const [step, setStep] = useState(1);
    const [name, setName] = useState('');
    const [path, setPath] = useState('');
    const [methods, setMethods] = useState<string[]>([]);
    const [effect, setEffect] = useState<EffectId | null>(null);
    const [slowMs, setSlowMs] = useState(1000);
    const [errorCode, setErrorCode] = useState(500);
    const [corruptFields, setCorruptFields] = useState('');
    const [attackId, setAttackId] = useState<AttackId | null>(null);
    const [frequency, setFrequency] = useState('always');
    const [error, setError] = useState('');
    const [creating, setCreating] = useState(false);

    // Endpoints the chaos session has actually been observed serving. Fetched
    // once when the wizard mounts (the wizard only mounts while open) and used
    // to populate Step A's suggestion picker. Empty list -> the wizard still
    // works: the manual path input remains, with a helper note.
    const [endpoints, setEndpoints] = useState<discovery.Endpoint[]>([]);

    useEffect(() => {
        let cancelled = false;
        GetDiscoveredEndpoints(CHAOS_SESSION_NAME).then(eps => {
            if (!cancelled) {
                setEndpoints(eps);
            }
        });
        return () => {
            cancelled = true;
        };
    }, []);

    // pickEndpoint fills the path when a suggestion is chosen. Method
    // pre-selection is only attempted when the observed endpoint is UNAMBIGUOUS
    // (exactly one distinct method seen for this normalized path) AND that
    // method is one the wizard/backend can express (GET/POST/PUT/DELETE/PATCH).
    // A path seen under multiple methods, or an exotic method like HEAD, means
    // we don't guess — the path is filled and the user picks the method.
    const pickEndpoint = (ep: discovery.Endpoint) => {
        setError('');
        setPath(ep.path);
        const seenMethods = Array.from(new Set(
            endpoints.filter(e => e.path === ep.path).map(e => e.method),
        ));
        if (seenMethods.length === 1 && METHOD_CHOICES.includes(seenMethods[0])) {
            setMethods([seenMethods[0]]);
        }
    };

    const corruptKeys = corruptFields.split(',').map(s => s.trim()).filter(Boolean);

    // Each step is validated on Next so a bad value can never silently carry
    // forward. The messages are written for a human reading them in the modal.
    const validateStep = (s: number): string => {
        switch (s) {
            case 1:
                if (name.trim() === '') return 'Give the rule a name so you can find it in the list later.';
                if (path.trim() === '') return 'Enter the path this rule should target (e.g. /api/checkout).';
                return '';
            case 2:
                if (!effect) return 'Pick one thing this rule should do.';
                if (effect === 'corrupt' && corruptKeys.length === 0) return 'Enter at least one JSON field name to remove.';
                if (effect === 'attack' && !attackId) return 'Pick an attack type.';
                return '';
            default:
                return '';
        }
    };

    const next = () => {
        const problem = validateStep(step);
        if (problem) {
            setError(problem);
            return;
        }
        setError('');
        setStep(step + 1);
    };

    const back = () => {
        setError('');
        setStep(step - 1);
    };

    const toggleMethod = (m: string) => {
        setError('');
        setMethods(prev => (prev.includes(m) ? prev.filter(x => x !== m) : [...prev, m]));
    };

    const create = async () => {
        // Defensive: nothing should reach step 4 unvalidated, but re-check the
        // collected fields before hitting the backend.
        const problem = validateStep(1) || validateStep(2);
        if (problem) {
            setError(problem);
            return;
        }
        setError('');
        setCreating(true);
        const result = await CreateRule(new config.CreateRuleRequest({
            name: name.trim(),
            path: path.trim(),
            methods,
            frequency,
            effect: effect as string,
            slowMs: effect === 'slow' ? slowMs : undefined,
            errorCode: effect === 'error' ? errorCode : undefined,
            targetKeys: effect === 'corrupt' ? corruptKeys : undefined,
            attack: effect === 'attack' ? (attackId ?? undefined) : undefined,
        }));
        setCreating(false);
        if (result === '') {
            onCreated({kind: 'success', name: name.trim()});
            return;
        }
        if (result.startsWith('warning:')) {
            onCreated({kind: 'warning', message: result.slice('warning:'.length).trim()});
            return;
        }
        // Hard failure: nothing was created — keep the wizard open so the user
        // can go Back, fix the input, and retry.
        setError(result);
    };

    // Build a RuleView from the form so the review block reuses the exact
    // plain-language helpers the list renders with (guaranteeing the preview
    // matches the eventual display format).
    const preview = new main.RuleView({
        name: name.trim(),
        path: path.trim(),
        pathRegex: '',
        methods,
        errorRate: FREQUENCY_RATES[frequency] ?? 0,
        enabled: true,
        hasLatency: effect === 'slow',
        hasStatusOverride: effect === 'error',
        hasDropConnection: effect === 'drop',
        hasMangle: effect === 'corrupt',
        hasFuzz: effect === 'attack',
    });
    const previewTags = chaosTags(preview);

    let previewDetail = '';
    switch (effect) {
        case 'slow':
            previewDetail = `Adds a ${slowMs}ms delay to the response.`;
            break;
        case 'error': {
            const c = ERROR_CODE_CHOICES.find(x => x.code === errorCode);
            previewDetail = `Returns ${c ? c.label : String(errorCode)} instead of the real response.`;
            break;
        }
        case 'drop':
            previewDetail = 'Drops the connection on matching requests.';
            break;
        case 'corrupt':
            previewDetail = `Removes these fields from the response: ${corruptKeys.join(', ') || 'none'}.`;
            break;
        case 'attack': {
            const a = ATTACK_CHOICES.find(x => x.id === attackId);
            previewDetail = a ? `Sends ${a.label} test payloads to matching requests.` : '';
            break;
        }
        default:
            previewDetail = '';
    }

    return (
        <div className="wizard-overlay" onClick={onClose}>
            <div className="wizard" role="dialog" aria-modal="true" aria-label="Create a new chaos rule" onClick={e => e.stopPropagation()}>
                <div className="wizard-step">STEP {step} OF 4</div>
                <div className="wizard-heading">
                    {step === 1 && 'What do you want to test?'}
                    {step === 2 && 'What should happen?'}
                    {step === 3 && 'How often should this happen?'}
                    {step === 4 && 'Review and create'}
                </div>

                {step === 1 && (
                    <div className="wizard-body">
                        <div className="settings-field">
                            <label htmlFor="wizard-name">Rule name</label>
                            <input
                                className="settings-input"
                                id="wizard-name"
                                type="text"
                                placeholder="e.g. Slow checkout page"
                                value={name}
                                onChange={e => setName(e.target.value)}
                            />
                        </div>
                        <div className="settings-field">
                            <label htmlFor="wizard-path">Endpoint / Path</label>
                            <EndpointCombo
                                value={path}
                                onChange={setPath}
                                endpoints={endpoints}
                                onPick={pickEndpoint}
                            />
                            {endpoints.length === 0 ? (
                                <span className="wizard-note">
                                    No traffic observed yet — start the Chaos session and browse your app, or type a path manually below.
                                </span>
                            ) : (
                                <span className="wizard-note">
                                    Pick an endpoint seen in real traffic, or type any path manually.
                                </span>
                            )}
                        </div>
                        <div className="settings-field">
                            <label>Request method</label>
                            <div className="wizard-chips">
                                <button
                                    type="button"
                                    className={`wizard-chip${methods.length === 0 ? ' wizard-chip--active' : ''}`}
                                    onClick={() => setMethods([])}
                                >
                                    Any request
                                </button>
                                {METHOD_CHOICES.map(m => (
                                    <button
                                        type="button"
                                        key={m}
                                        className={`wizard-chip${methods.includes(m) ? ' wizard-chip--active' : ''}`}
                                        onClick={() => toggleMethod(m)}
                                    >
                                        {m}
                                    </button>
                                ))}
                            </div>
                        </div>
                    </div>
                )}

                {step === 2 && (
                    <div className="wizard-body">
                        <div className="stress-presets wizard-presets">
                            {EFFECT_CARDS.map(c => {
                                const Icon = c.icon;
                                return (
                                    <button
                                        type="button"
                                        key={c.id}
                                        className={`stress-preset wizard-card${effect === c.id ? ' wizard-card--active' : ''}`}
                                        onClick={() => setEffect(c.id)}
                                    >
                                        <span className="stress-preset-icon"><Icon size={24} color={c.color} strokeWidth={1.8} aria-hidden="true" /></span>
                                        <span className="stress-preset-name">{c.name}</span>
                                        <span className="stress-preset-desc">{c.desc}</span>
                                    </button>
                                );
                            })}
                        </div>

                        {effect === 'slow' && (
                            <div className="wizard-extra">
                                <div className="settings-field">
                                    <label htmlFor="wizard-slow">Delay (ms)</label>
                                    <input
                                        className="settings-input"
                                        id="wizard-slow"
                                        type="number"
                                        min={100}
                                        max={10000}
                                        step={50}
                                        value={slowMs}
                                        onChange={e => setSlowMs(e.target.value === '' ? 1000 : Number(e.target.value))}
                                    />
                                    <span className="wizard-note">Add this many milliseconds of delay to each matching response.</span>
                                </div>
                            </div>
                        )}

                        {effect === 'error' && (
                            <div className="wizard-extra">
                                <div className="settings-field">
                                    <label>Error status</label>
                                    <div className="wizard-chips">
                                        {ERROR_CODE_CHOICES.map(c => (
                                            <button
                                                type="button"
                                                key={c.code}
                                                className={`wizard-chip${errorCode === c.code ? ' wizard-chip--active' : ''}`}
                                                onClick={() => setErrorCode(c.code)}
                                            >
                                                {c.label}
                                            </button>
                                        ))}
                                    </div>
                                    <span className="wizard-note">Return this status instead of the real response.</span>
                                </div>
                            </div>
                        )}

                        {effect === 'corrupt' && (
                            <div className="wizard-extra">
                                <div className="settings-field">
                                    <label htmlFor="wizard-corrupt">Fields to remove</label>
                                    <input
                                        className="settings-input"
                                        id="wizard-corrupt"
                                        type="text"
                                        placeholder="e.g. email, address"
                                        value={corruptFields}
                                        onChange={e => setCorruptFields(e.target.value)}
                                    />
                                    <span className="wizard-note">
                                        Comma-separated — these should match JSON field names in the actual API response.
                                    </span>
                                </div>
                            </div>
                        )}

                        {effect === 'attack' && (
                            <div className="wizard-extra">
                                <div className="settings-field">
                                    <label>Attack type</label>
                                    <div className="wizard-chips wizard-chips--stacked">
                                        {ATTACK_CHOICES.map(a => (
                                            <button
                                                type="button"
                                                key={a.id}
                                                className={`wizard-chip wizard-chip--tall${attackId === a.id ? ' wizard-chip--active' : ''}`}
                                                onClick={() => setAttackId(a.id)}
                                            >
                                                <span className="wizard-chip-label">{a.label}</span>
                                                <span className="wizard-chip-desc">{a.desc}</span>
                                            </button>
                                        ))}
                                    </div>
                                </div>
                            </div>
                        )}
                    </div>
                )}

                {step === 3 && (
                    <div className="wizard-body">
                        <div className="stress-presets wizard-presets">
                            {FREQUENCY_CARDS.map(f => (
                                <button
                                    type="button"
                                    key={f.id}
                                    className={`stress-preset wizard-card${frequency === f.id ? ' wizard-card--active' : ''}`}
                                    onClick={() => setFrequency(f.id)}
                                >
                                    <span className="stress-preset-name">{f.label}</span>
                                    <span className="stress-preset-desc">{f.desc}</span>
                                </button>
                            ))}
                        </div>
                    </div>
                )}

                {step === 4 && (
                    <div className="wizard-body">
                        <span className="wizard-note">This is how the rule will appear in the list below once created.</span>
                        <div className="wizard-review">
                            <div className="chaos-row-summary">{chaosSummary(preview)}</div>
                            <div className="chaos-row-frequency">{chaosFrequencyText(preview.errorRate || 0)}</div>
                            {previewTags.length > 0 && (
                                <div className="chaos-row-tags">
                                    {previewTags.map(t => (
                                        <ChaosTagPill key={t.kind} tag={t} />
                                    ))}
                                </div>
                            )}
                            <div className="wizard-review-detail">{previewDetail}</div>
                        </div>
                    </div>
                )}

                <div className="wizard-nav">
                    <button type="button" className="btn" onClick={onClose}>Cancel</button>
                    <div className="wizard-nav-right">
                        {step > 1 && (
                            <button type="button" className="btn" onClick={back}>BACK</button>
                        )}
                        {step < 4 ? (
                            <button type="button" className="btn" onClick={next}>NEXT</button>
                        ) : (
                            <button type="button" className="btn" onClick={create} disabled={creating}>
                                {creating ? 'CREATING…' : 'CREATE RULE'}
                            </button>
                        )}
                    </div>
                </div>

                {error && <div className="wizard-error">{error}</div>}
            </div>
        </div>
    );
}

// ---------- stress test ----------

type StressPhase = 'pick' | 'running' | 'finished';
type PresetId = 'light' | 'viral' | 'stressbreak';
type StressTone = 'good' | 'warn' | 'bad';

interface HistoryPoint {
    label: string;
    rps: number;
    errorRate: number;
}

function stressTone(errorRate: number): StressTone {
    if (errorRate >= 0.5) return 'bad';
    if (errorRate >= 0.1) return 'warn';
    return 'good';
}

// Tone -> severity color, reused for the radial error ring (Task 39's coloring).
const TONE_COLOR: Record<StressTone, string> = {
    good: 'var(--severity-good)',
    warn: 'var(--severity-warning)',
    bad: 'var(--severity-critical)',
};

// Path for a 270-degree gauge arc (opening at the bottom), centered (60,60),
// radius 48, within a 120x120 viewBox. pathLength=100 normalizes every arc to
// 0..100 so stroke-dasharray/dashoffset work in percent units regardless of
// geometry (the same trick powers both the gauge and the full-ring variant).
const GAUGE_ARC_270 = 'M 26.06 93.94 A 48 48 0 1 1 93.94 93.94';

// SVG radial gauge: a 270-degree dial drawn with stroke-dasharray/offset on a
// normalized path. The value text sits centered inside the arc (the reference
// dashboard's "big number + label below" dial pattern). The center numbers are
// ALWAYS --text-primary regardless of the arc color: the ring's accent/severity
// color drives only the stroke, never the text fill, so the value never turns
// dark/illegible against a saturated arc.
function RadialGauge({value, min, max, label, color, display}: {
    value: number;
    min: number;
    max: number;
    label: string;
    color: string;
    display: string;
}) {
    const frac = max > min ? Math.min(1, Math.max(0, (value - min) / (max - min))) : 0;
    const dash = 100 - frac * 100;
    return (
        <div className="radial-wrap">
            <svg viewBox="0 0 120 120" className="radial-svg" role="img" aria-label={`${label}: ${display}`}>
                <path d={GAUGE_ARC_270} pathLength={100} className="radial-arc" strokeWidth={10} strokeLinecap="round" />
                <path
                    d={GAUGE_ARC_270}
                    pathLength={100}
                    className="radial-arc radial-arc--value"
                    strokeWidth={10}
                    strokeLinecap="round"
                    style={{stroke: color, strokeDasharray: 100, strokeDashoffset: dash}}
                />
                <text x="60" y="56" className="radial-value" textAnchor="middle">{display}</text>
                <text x="60" y="76" className="radial-label" textAnchor="middle">{label}</text>
            </svg>
        </div>
    );
}

// SVG radial ring: a full-circle 0-100% progress ring (stroke-dasharray/offset
// on a pathLength-normalized circle), with the percentage centered inside. Same
// contrast rule as the gauge: the color prop colors the stroke only.
function RadialRing({percentage, label, color, display}: {
    percentage: number;
    label: string;
    color: string;
    display: string;
}) {
    const pct = Math.min(100, Math.max(0, percentage));
    const dash = 100 - pct;
    return (
        <div className="radial-wrap">
            <svg viewBox="0 0 120 120" className="radial-svg" role="img" aria-label={`${label}: ${display}`}>
                <circle cx="60" cy="60" r="48" pathLength={100} className="radial-arc" strokeWidth={10} strokeLinecap="round" fill="none" />
                <circle
                    cx="60"
                    cy="60"
                    r="48"
                    pathLength={100}
                    className="radial-arc radial-arc--value"
                    strokeWidth={10}
                    strokeLinecap="round"
                    fill="none"
                    style={{stroke: color, strokeDasharray: 100, strokeDashoffset: dash}}
                />
                <text x="60" y="54" className="radial-value" textAnchor="middle">{display}</text>
                <text x="60" y="72" className="radial-label" textAnchor="middle">{label}</text>
            </svg>
        </div>
    );
}

// Humanize a backend error-category label for the breakdown list:
// "connection_refused" -> "Connection Refused", "status_503" -> "503 Service
// Unavailable" (with the standard HTTP reason phrase), "status_599" -> "Status 599".
const HTTP_STATUS_REASONS: Record<number, string> = {
    500: 'Internal Server Error',
    501: 'Not Implemented',
    502: 'Bad Gateway',
    503: 'Service Unavailable',
    504: 'Gateway Timeout',
    505: 'HTTP Version Not Supported',
    507: 'Insufficient Storage',
    508: 'Loop Detected',
    510: 'Not Extended',
    511: 'Network Authentication Required',
};
function humanizeCategory(cat: string): string {
    if (cat.startsWith('status_')) {
        const code = Number(cat.slice('status_'.length));
        const reason = HTTP_STATUS_REASONS[code];
        return reason ? `${code} ${reason}` : `Status ${code}`;
    }
    return cat.split('_').map(w => (w ? w.charAt(0).toUpperCase() + w.slice(1) : w)).join(' ');
}

interface PresetDef {
    id: PresetId;
    icon: LucideIcon;
    color: string;
    name: string;
    desc: string;
}

function StressTest() {
    const [phase, setPhase] = useState<StressPhase>('pick');
    const [latest, setLatest] = useState<StressUpdatePayload | null>(null);
    const [history, setHistory] = useState<HistoryPoint[]>([]);
    const [launchError, setLaunchError] = useState('');

    // Default target for presets AND the Custom form comes from settings.
    const [settingsTarget, setSettingsTarget] = useState('http://localhost:3000');
    const [form, setForm] = useState({
        targetURL: '',
        targetRPS: '50',
        durationSec: '30',
        concurrency: '10',
        shape: '',
        endRPS: '',
        spikeRPS: '',
    });

    // Which preset started the current run (null = Custom). Only 'stressbreak'
    // enables the frontend auto-stop rule, so a bad error rate never aborts the
    // other presets. Refs keep the event handler stable (no resubscribes).
    const presetRef = useRef<PresetId | null>(null);
    const autoStoppedRef = useRef(false);
    // Configured TargetRPS for the current run — feeds the RPS gauge's scale.
    const targetRpsRef = useRef(50);

    const handleUpdate = useCallback((u: StressUpdatePayload) => {
        setLatest(u);
        setHistory(prev => {
            const next = [...prev, {label: new Date().toLocaleTimeString(), rps: u.rps, errorRate: u.errorRate}];
            return next.length > 30 ? next.slice(next.length - 30) : next;
        });
        if (u.final) {
            setPhase('finished');
            return;
        }
        // Stress & Break: the backend has no conditional auto-stop, so the
        // frontend watches the live errorRate and reuses the real ABORT path
        // (StopStressTest) once 50% errors is crossed. Guarded so the abort is
        // issued exactly once per run.
        if (presetRef.current === 'stressbreak' && !autoStoppedRef.current && u.errorRate >= 0.5) {
            autoStoppedRef.current = true;
            StopStressTest();
        }
    }, []);

    // Subscribe only while the Running/Results view is active; unsub on leave.
    useEffect(() => {
        if (phase !== 'running') return;
        const off = EventsOn('stress_update', (data: any) => handleUpdate(data as StressUpdatePayload));
        return off;
    }, [phase, handleUpdate]);

    // Seed defaults. Also cover the case of navigating here while a test from a
    // previous visit is still running: jump straight into the Running view and
    // resume live updates.
    useEffect(() => {
        GetSettings().then(s => {
            setSettingsTarget(s.targetURL);
            setForm(prev => ({...prev, targetURL: s.targetURL}));
        });
        GetStressStatus().then(runningNow => {
            if (runningNow) {
                presetRef.current = null;
                setPhase('running');
            }
        });
    }, []);

    const launch = async (preset: PresetId | null, cfg: main.StressConfig) => {
        setLaunchError('');
        const err = await StartStressTest(cfg);
        if (err !== '') {
            setLaunchError(err);
            return;
        }
        presetRef.current = preset;
        autoStoppedRef.current = false;
        targetRpsRef.current = cfg.targetRPS;
        setLatest(null);
        setHistory([]);
        setPhase('running');
    };

    const launchPreset = (p: PresetId) => {
        const t = settingsTarget || 'http://localhost:3000';
        let cfg: main.StressConfig;
        switch (p) {
            case 'light':
                cfg = new main.StressConfig({targetURL: t, targetRPS: 50, durationSec: 30, concurrency: 10, shape: '', endRPS: 0, spikeRPS: 0});
                break;
            case 'viral':
                // Backend hardcodes spikeStart=0 / spikeDuration=5s (not exposed
                // via StressConfig), so the spike lands in the FIRST 5s of the
                // 20s run — the subtext below says exactly that.
                cfg = new main.StressConfig({targetURL: t, targetRPS: 20, durationSec: 20, concurrency: 30, shape: 'spike', endRPS: 0, spikeRPS: 500});
                break;
            case 'stressbreak':
                cfg = new main.StressConfig({targetURL: t, targetRPS: 10, durationSec: 60, concurrency: 50, shape: 'continuous', endRPS: 1000, spikeRPS: 0});
                break;
            default:
                return;
        }
        launch(p, cfg);
    };

    const presets: PresetDef[] = [
        {id: 'light', icon: Zap, color: 'var(--severity-good)', name: 'Light Load', desc: 'Steady 50 RPS for 30 seconds'},
        {id: 'viral', icon: TrendingUp, color: 'var(--severity-warning)', name: 'Viral Spike', desc: 'Baseline 20 RPS — spikes to 500 RPS immediately for the first 5s of a 20s run, then settles back down'},
        {id: 'stressbreak', icon: Flame, color: 'var(--severity-critical)', name: 'Stress & Break', desc: 'Ramps up traffic until your backend starts struggling — auto-stops at a 50% error rate'},
    ];

    const num = (s: string) => Number(s);
    const formValid =
        form.targetURL.trim() !== '' &&
        num(form.targetRPS) > 0 &&
        num(form.durationSec) > 0 &&
        num(form.concurrency) > 0 &&
        (form.shape !== 'continuous' || num(form.endRPS) > 0) &&
        (form.shape !== 'spike' || num(form.spikeRPS) > 0);

    const launchCustom = () => {
        const cfg = new main.StressConfig({
            targetURL: form.targetURL.trim(),
            targetRPS: num(form.targetRPS),
            durationSec: Math.floor(num(form.durationSec)),
            concurrency: Math.floor(num(form.concurrency)),
            shape: form.shape,
            endRPS: form.shape === 'continuous' ? num(form.endRPS) : 0,
            spikeRPS: form.shape === 'spike' ? num(form.spikeRPS) : 0,
        });
        launch(null, cfg);
    };

    const setField = (key: keyof typeof form) => (e: ChangeEvent<HTMLInputElement | HTMLSelectElement>) => {
        setForm(prev => ({...prev, [key]: e.target.value}));
    };

    if (phase === 'pick') {
        return (
            <div className="stress">
                <div className="stress-title">STRESS TEST — PRESETS</div>

                <div className="stress-presets">
                    {presets.map(p => {
                        const Icon = p.icon;
                        return (
                            <button className="stress-preset" key={p.id} onClick={() => launchPreset(p.id)}>
                                <span className="stress-preset-icon"><Icon size={24} color={p.color} strokeWidth={1.8} aria-hidden="true" /></span>
                                <span className="stress-preset-name">{p.name}</span>
                                <span className="stress-preset-desc">{p.desc}</span>
                            </button>
                        );
                    })}
                </div>

                <div className="stress-custom-heading">CUSTOM CONFIGURATION</div>

                <form
                    className="settings-form stress-form"
                    onSubmit={e => {
                        e.preventDefault();
                        if (formValid) launchCustom();
                    }}
                >
                    <div className="settings-field">
                        <label htmlFor="stress-target">Target URL</label>
                        <input
                            className="settings-input"
                            id="stress-target"
                            type="text"
                            placeholder="http://localhost:3000"
                            value={form.targetURL}
                            onChange={setField('targetURL')}
                        />
                    </div>

                    <div className="settings-field-stress-row">
                        <div className="settings-field">
                            <label htmlFor="stress-rps">Target RPS</label>
                            <input
                                className="settings-input"
                                id="stress-rps"
                                type="number"
                                min={1}
                                value={form.targetRPS}
                                onChange={setField('targetRPS')}
                            />
                        </div>
                        <div className="settings-field">
                            <label htmlFor="stress-duration">Duration (s)</label>
                            <input
                                className="settings-input"
                                id="stress-duration"
                                type="number"
                                min={1}
                                value={form.durationSec}
                                onChange={setField('durationSec')}
                            />
                        </div>
                        <div className="settings-field">
                            <label htmlFor="stress-concurrency">Concurrency</label>
                            <input
                                className="settings-input"
                                id="stress-concurrency"
                                type="number"
                                min={1}
                                value={form.concurrency}
                                onChange={setField('concurrency')}
                            />
                        </div>
                    </div>

                    <div className="settings-field">
                        <label htmlFor="stress-shape">Shape</label>
                        <select
                            className="settings-input"
                            id="stress-shape"
                            value={form.shape}
                            onChange={setField('shape')}
                        >
                            <option value="">None (flat)</option>
                            <option value="continuous">Continuous</option>
                            <option value="stepped">Stepped</option>
                            <option value="spike">Spike</option>
                        </select>
                    </div>

                    {form.shape === 'continuous' && (
                        <div className="settings-field">
                            <label htmlFor="stress-end-rps">End RPS</label>
                            <input
                                className="settings-input"
                                id="stress-end-rps"
                                type="number"
                                min={1}
                                placeholder="1000"
                                value={form.endRPS}
                                onChange={setField('endRPS')}
                            />
                        </div>
                    )}

                    {form.shape === 'spike' && (
                        <div className="settings-field">
                            <label htmlFor="stress-spike-rps">Spike RPS</label>
                            <input
                                className="settings-input"
                                id="stress-spike-rps"
                                type="number"
                                min={1}
                                placeholder="500"
                                value={form.spikeRPS}
                                onChange={setField('spikeRPS')}
                            />
                        </div>
                    )}

                    <div className="settings-actions">
                        <button className="btn" type="submit" disabled={!formValid}>
                            Launch
                        </button>
                        {launchError && <span className="settings-error">{launchError}</span>}
                    </div>
                </form>
            </div>
        );
    }

    // Running or finished — the shared Metrics/Results view.
    const status = phase === 'running' ? 'RUNNING' : 'FINISHED';
    const errorRate = latest?.errorRate ?? 0;
    const tone = stressTone(errorRate);
    const rpsVal = latest?.rps ?? 0;
    // Gauge ceiling: headroom (1.1x) above the run's peak RPS seen so far AND
    // the configured target — so it reads a sensible scale during ramp-up yet
    // never clips a legitimate spike (e.g. Viral Spike's 500 RPS vs 20 target).
    const rpsMax = Math.max(...history.map(h => h.rps), targetRpsRef.current, 1) * 1.1;
    const errPct = Math.min(100, errorRate * 100);
    // Error category breakdown: only when a live breakdown actually exists
    // (older backends and zero-error runs omit errorCategories entirely). Sorted
    // by count descending, most frequent failure first.
    const errorCats: [string, number][] | null =
        latest && errorRate > 0 && latest.errorCategories
            ? Object.entries(latest.errorCategories).sort((a, b) => b[1] - a[1])
            : null;

    const pctCards = [
        {label: 'p50', value: latest ? `${latest.p50Ms}ms` : '—', note: 'Half of requests are this fast or faster'},
        {label: 'p95', value: latest ? `${latest.p95Ms}ms` : '—', note: '95% of requests are this fast or faster'},
        {label: 'p99', value: latest ? `${latest.p99Ms}ms` : '—', note: '99% of requests are this fast or faster'},
    ];

    return (
        <div className="stress">
            <div className="stress-title">STRESS TEST — {status}</div>

            <div className={`stress-top stress-top--${tone}`}>
                <div className="stress-metric">
                    <div className="stress-metric-value">{Math.round(rpsVal)}</div>
                    <div className="stress-metric-label">RPS</div>
                </div>
                <div className="stress-metric">
                    <div className="stress-metric-value">{Math.round(errorRate * 100)}%</div>
                    <div className="stress-metric-label">ERROR RATE</div>
                </div>
                <span className={`stress-badge${status === 'RUNNING' ? ' stress-badge--running' : ''}`}>
                    {status === 'RUNNING' ? '● RUNNING' : 'FINISHED'}
                </span>
            </div>

            <div className="stress-feed">
                <div className="stress-dials">
                    <RadialGauge
                        value={rpsVal}
                        min={0}
                        max={rpsMax}
                        label="RPS"
                        color="var(--accent-primary)"
                        display={String(Math.round(rpsVal))}
                    />
                    <RadialRing
                        percentage={errPct}
                        label="ERROR RATE"
                        color={TONE_COLOR[tone]}
                        display={`${Math.round(errorRate * 100)}%`}
                    />
                </div>
                {errorCats && errorCats.length > 0 && (
                    <div className="stress-cats">
                        <div className="stress-cats-head">ERROR BREAKDOWN</div>
                        {errorCats.map(([name, count]) => (
                            <div className="stress-cat-row" key={name}>
                                <span className="stress-cat-name">{humanizeCategory(name)}</span>
                                <span className="stress-cat-count">{count}</span>
                            </div>
                        ))}
                    </div>
                )}
                <div className="stress-feed-meta">
                    Live trace — last {history.length} update{history.length === 1 ? '' : 's'} ·{' '}
                    {latest ? `${latest.totalRequests} requests · ${latest.totalErrors} errors` : 'waiting for data…'}
                </div>
            </div>

            <div className="stress-pct">
                {pctCards.map(c => (
                    <div className="stress-pct-card" key={c.label}>
                        <div className="stress-pct-label">{c.label}</div>
                        <div className="stress-pct-value">{c.value}</div>
                        <div className="stress-pct-note">{c.note}</div>
                    </div>
                ))}
            </div>

            {status === 'RUNNING' ? (
                <button className="btn btn--danger stress-abort" onClick={() => StopStressTest()}>
                    ABORT TEST
                </button>
            ) : (
                <button
                    className="btn stress-again"
                    onClick={() => {
                        setPhase('pick');
                        setLaunchError('');
                    }}
                >
                    Run Another Test
                </button>
            )}
        </div>
    );
}

// A security-finding log row with an expandable "How to fix" detail. Collapsed
// by default so the feed stays scannable at a glance; clicking the row (or
// pressing Enter/Space while focused) reveals the remediation text beneath the
// one-line summary. Rows without remediation (defensive: an older backend) stay
// plain and non-interactive. Shared by the Security tab and the Dashboard's
// combined log so the expand behavior and markup stay identical; the Dashboard
// opts into the per-session badge via showSession.
function FindingLogRow({entry, showSession = false}: {entry: LogEntry; showSession?: boolean}) {
    const [expanded, setExpanded] = useState(false);
    const hasFix = !!entry.remediation;
    const toggle = () => {
        if (hasFix) setExpanded(v => !v);
    };
    const cls = ['log-entry', 'log-entry--finding']
        .concat(hasFix ? ['log-entry--expandable'] : [])
        .concat(expanded ? ['log-entry--expanded'] : [])
        .join(' ');
    return (
        <div
            className={cls}
            onClick={toggle}
            role={hasFix ? 'button' : undefined}
            tabIndex={hasFix ? 0 : undefined}
            aria-expanded={hasFix ? expanded : undefined}
            onKeyDown={e => {
                if (!hasFix) return;
                if (e.key === 'Enter' || e.key === ' ') {
                    e.preventDefault();
                    toggle();
                }
            }}
            title={hasFix ? (expanded ? 'Hide how to fix this finding' : 'Show how to fix this finding') : undefined}
        >
            <span className="log-time">{entry.time}</span>
            {showSession && entry.sessionName && (
                <span className={`log-session log-session--${entry.sessionName}`}>
                    {entry.sessionName === CHAOS_SESSION_NAME ? 'CHAOS' : 'SECURITY'}
                </span>
            )}
            <span className={`log-dot log-dot--${entry.severity}`} />
            <span className="log-summary">{entry.summary}</span>
            {hasFix && (
                <span className={`log-chevron${expanded ? ' log-chevron--open' : ''}`} aria-hidden="true">
                    <ChevronDown size={14} strokeWidth={2} />
                </span>
            )}
            {expanded && hasFix && (
                <div className="log-remediation">
                    <span className="log-remediation-label">HOW TO FIX</span>
                    <div className="log-remediation-text">{entry.remediation}</div>
                </div>
            )}
        </div>
    );
}

// SecurityView is a dedicated, self-contained view for the "security" session
// (an independent proxy session that runs ONLY the passive scanners, isolated
// from the chaos session). It has its own Start/Stop controls, its own status
// badge, and subscribes to the shared "security_finding" event stream while
// running so findings render live without disturbing other views.
function SecurityView() {
    const [running, setRunning] = useState(false);
    const [busy, setBusy] = useState(false);
    const [error, setError] = useState('');
    const [findings, setFindings] = useState<LogEntry[]>([]);
    const findingId = useRef(0);

    // Poll session status like other views refresh their state on an interval.
    useEffect(() => {
        let cancelled = false;
        const poll = () => {
            GetSessionStatus(SECURITY_SESSION_NAME).then(s => {
                if (!cancelled) setRunning(s);
            });
        };
        poll();
        const id = window.setInterval(poll, 1000);
        return () => {
            cancelled = true;
            window.clearInterval(id);
        };
    }, []);

    // Subscribe to the live security_finding stream while the session runs;
    // unsubscribe (and clear findings) when stopped or leaving the view.
    useEffect(() => {
        if (!running) {
            setFindings([]);
            return;
        }
        const off = EventsOn('security_finding', (data: any) => {
            const f = data as FindingPayload;
            findingId.current += 1;
            const id = findingId.current;
            const entry: LogEntry = {
                id,
                time: new Date().toLocaleTimeString(),
                tsMs: Date.now(),
                severity: normalizeSeverity(f.FindingSeverity),
                summary: `${f.FindingCategory}: ${f.Detail}`,
                sessionName: SECURITY_SESSION_NAME,
                remediation: f.remediation,
            };
            setFindings(prev => [entry, ...prev].slice(0, 200));
        });
        return off;
    }, [running]);

    const start = () => {
        setBusy(true);
        setError('');
        StartSession(SECURITY_SESSION_NAME).then(err => {
            setBusy(false);
            if (err) {
                setError(err);
                return;
            }
            GetSessionStatus(SECURITY_SESSION_NAME).then(setRunning);
        });
    };

    const stop = () => {
        setBusy(true);
        setError('');
        StopSession(SECURITY_SESSION_NAME).then(err => {
            setBusy(false);
            if (err) {
                setError(err);
                return;
            }
            GetSessionStatus(SECURITY_SESSION_NAME).then(setRunning);
        });
    };

    return (
        <div className="security">
            <div className="security-title">SECURITY SCAN</div>
            <div className="security-desc">
                Passively checks your backend's responses for missing security headers and leaked
                secrets, without touching your traffic.
            </div>

            <div className="security-controls">
                <div className="security-status">
                    <span className={`status-dot${running ? ' status-dot--running' : ''}`} />
                    <span className="security-status-text">{running ? 'Running' : 'Stopped'}</span>
                </div>
                <button className="btn btn--primary" onClick={start} disabled={busy || running}>
                    Start Scan
                </button>
                <button className="btn btn--danger" onClick={stop} disabled={busy || !running}>
                    Stop Scan
                </button>
                <span className="security-port">port {SECURITY_SESSION_PORT}</span>
                {error && <span className="settings-error">{error}</span>}
            </div>

            <div className="security-log">
                <div className="security-log-title">LIVE FINDINGS</div>
                <div className="log-panel">
                    {findings.length === 0 && (
                        <div className="log-empty">
                            No findings yet. Start a scan and send some traffic to your backend to see
                            results here.
                        </div>
                    )}
                    {findings.map(e => (
                        <FindingLogRow entry={e} key={e.id} />
                    ))}
                </div>
            </div>
        </div>
    );
}

// A single "smart auto-suggestion" badge on a clean traffic event. Clicking it
// injects a pre-filled chaos rule via the shared CreateRule binding; the badge
// mirrors the async outcome inline (Adding… / Added / Failed) so a beginner
// always knows what happened without leaving the Dashboard. The action glyph is
// the idle icon, replaced by a Check on success and an AlertTriangle on failure.
function SuggestionBadge({label, state, onClick, icon}: {
    label: string;
    state: {status: 'idle' | 'creating' | 'added' | 'error'; message?: string};
    onClick: () => void;
    icon: LucideIcon;
}) {
    const DisplayedIcon: LucideIcon = state.status === 'added'
        ? Check
        : state.status === 'error'
        ? AlertTriangle
        : icon;
    const text = state.status === 'creating'
        ? 'Adding…'
        : state.status === 'added'
        ? 'Added'
        : state.status === 'error'
        ? 'Failed'
        : label;
    const cls = ['suggestion-badge']
        .concat(state.status === 'creating' ? ['suggestion-badge--creating'] : [])
        .concat(state.status === 'added' ? ['suggestion-badge--added'] : [])
        .concat(state.status === 'error' ? ['suggestion-badge--error'] : [])
        .join(' ');
    return (
        <button className={cls} onClick={onClick} disabled={state.status === 'creating'} title={state.message}>
            <DisplayedIcon size={12} strokeWidth={2.5} aria-hidden="true" />
            {text}
        </button>
    );
}

// A "Damage Control Certificate" — the printable/exportable summary of the
// active testing session. High-contrast monochrome so a manual screenshot or
// the Markdown export both read cleanly in a PR or report.
function ScorecardModal({stats, onClose, onCopy, copied, stressRunning, stressCompletedAt}: {
    stats: ScorecardStats;
    onClose: () => void;
    onCopy: () => void;
    copied: boolean;
    stressRunning: boolean;
    stressCompletedAt: string | null;
}) {
    const pct = (v: number) => `${Math.round(v * 100)}%`;
    const empty = stats.traffic.total === 0 && stats.security.findingCount === 0;
    const perfStatus = stats.performance.present
        ? stressRunning
            ? SCORECARD_PERF_LIVE
            : stressCompletedAt
            ? SCORECARD_PERF_COMPLETED(stressCompletedAt)
            : 'Latest stress test'
        : '';
    return (
        <div className="scorecard-overlay" onClick={onClose}>
            <div className="scorecard" onClick={e => e.stopPropagation()} role="dialog" aria-modal="true">
                <div className="scorecard-head">
                    <div className="scorecard-title">DAMAGE CONTROL CERTIFICATE</div>
                    <div className="scorecard-subtitle">cha0s;sim — testing session summary</div>
                </div>

                <div className="scorecard-disclosure">
                    {SCORECARD_SCOPE_NOTE}
                </div>

                {empty && (
                    <div className="scorecard-empty">
                        No traffic or findings recorded yet. Start a chaos/security session and send some
                        requests before generating a report.
                    </div>
                )}

                <div className="scorecard-grid">
                    <section className="scorecard-section">
                        <div className="scorecard-section-title">TRAFFIC HEALTH</div>
                        <dl className="scorecard-list">
                            <div className="scorecard-row">
                                <dt>Requests processed</dt>
                                <dd>{stats.traffic.total}</dd>
                            </div>
                            <div className="scorecard-row">
                                <dt>Mutated requests</dt>
                                <dd>{stats.traffic.mutated}</dd>
                            </div>
                            <div className="scorecard-row">
                                <dt>Chaos hit rate</dt>
                                <dd>{pct(stats.traffic.hitRate)}</dd>
                            </div>
                        </dl>
                    </section>

                    <section className="scorecard-section">
                        <div className="scorecard-section-title">SECURITY HEALTH</div>
                        <dl className="scorecard-list">
                            <div className="scorecard-row">
                                <dt>Missing / weak headers</dt>
                                <dd>{stats.security.missingHeaders}</dd>
                            </div>
                            <div className="scorecard-row">
                                <dt>Leaked secrets</dt>
                                <dd>{stats.security.leakedSecrets}</dd>
                            </div>
                            <div className="scorecard-row">
                                <dt>Total findings</dt>
                                <dd>{stats.security.findingCount}</dd>
                            </div>
                        </dl>
                    </section>

                    <section className="scorecard-section">
                        <div className="scorecard-section-title">PERFORMANCE</div>
                        {stats.performance.present && perfStatus && (
                            <div className="scorecard-muted">{perfStatus}</div>
                        )}
                        {stats.performance.present ? (
                            <dl className="scorecard-list">
                                <div className="scorecard-row">
                                    <dt>p50 latency</dt>
                                    <dd>{stats.performance.p50Ms} ms</dd>
                                </div>
                                <div className="scorecard-row">
                                    <dt>p95 latency</dt>
                                    <dd>{stats.performance.p95Ms} ms</dd>
                                </div>
                                <div className="scorecard-row">
                                    <dt>p99 latency</dt>
                                    <dd>{stats.performance.p99Ms} ms</dd>
                                </div>
                                <div className="scorecard-row">
                                    <dt>RPS</dt>
                                    <dd>{Math.round(stats.performance.rps)}</dd>
                                </div>
                                <div className="scorecard-row">
                                    <dt>Error rate</dt>
                                    <dd>{pct(stats.performance.errorRate)}</dd>
                                </div>
                            </dl>
                        ) : (
                            <div className="scorecard-muted">No stress data recorded.</div>
                        )}
                    </section>
                </div>

                <div className="scorecard-foot">
                    <div className="scorecard-meta">
                        Generated {stats.generatedAt}
                    </div>
                    <div className="scorecard-actions">
                        <button className="btn" onClick={onCopy}>
                            {copied ? 'Copied!' : 'Copy Report to Clipboard'}
                        </button>
                        <button className="btn btn--danger" onClick={onClose}>
                            Close
                        </button>
                    </div>
                </div>
            </div>
        </div>
    );
}

// ---------- help / faq ----------

// FAQ answers are written in the same plain, no-jargon register as the rest of
// the UI and were verified against the app's actual behavior when written.
const HELP_FAQS: {q: string; a: string}[] = [
    {
        q: 'Do I need to write a config file?',
        a: "No. Until a chaos.yaml exists the proxy runs in passthrough mode — it forwards traffic cleanly without touching it. Create rules with the Rule Wizard or by clicking a suggestion badge, and the app writes the config file for you. Editing the YAML by hand is only needed for advanced options the wizard doesn't offer, like regex paths or custom delay jitter.",
    },
    {
        q: 'What happens when I turn off a rule?',
        a: "It stops firing immediately for new traffic, and the change is saved to your config file. If the file couldn't be written (rare), you'll see a soft amber warning and the change only sticks for this session.",
    },
    {
        q: 'Can I run Chaos and Security at the same time?',
        a: "Yes. They are independent sessions on different ports (8081 for Chaos, 8082 for Security) that don't interfere with each other.",
    },
    {
        q: "Why don't my chaos rules ever fire?",
        a: "A rule only fires when it's enabled AND has a frequency set — a rule showing 'Never triggers' (0% error rate) never fires. Then make sure the request's path and method match the rule, and that traffic is actually going through the session's port (8081). Low-frequency rules (about 1 in 20) can also just miss small amounts of traffic.",
    },
    {
        q: 'Does this tool store or transmit my data anywhere?',
        a: "No. Everything runs locally on your machine — there's no telemetry and nothing is sent externally. The only outbound traffic is the copies of your requests the proxy forwards to the backend you configured, and stress-test traffic to the target you set.",
    },
    {
        q: 'Why is my "Copy as cURL" command missing headers or a request body?',
        a: "Known, documented limitation: traffic events only capture the method and URL path — not headers, body, or query string. The copied command is accurate for that method + URL, but it can't reconstruct the full request.",
    },
    {
        q: 'What does the Scorecard\'s "chaos hit rate" mean?',
        a: "The share of requests a chaos effect actually landed on — a 20% hit rate means 2 in 10 of the requests shown got slowed, errored, or corrupted. It's calculated from the last 200 events in the live feed, not your entire session.",
    },
    {
        q: 'Does Stress Test use the same sessions?',
        a: "No. Stress Test talks directly to the target URL from Settings and creates its own traffic, so it can run at the same time as Chaos and Security without affecting them or their ports.",
    },
];

// The FAQ rows reuse the exact expandable-row pattern from the "HOW TO FIX"
// remediation rows (same ChevronDown chevron that rotates when opened).
function FaqItem({question, answer}: {question: string; answer: string}) {
    const [open, setOpen] = useState(false);
    return (
        <div className={`faq-item${open ? ' faq-item--open' : ''}`}>
            <button className="faq-question" onClick={() => setOpen(v => !v)} aria-expanded={open}>
                <span className="faq-question-text">{question}</span>
                <span className={`log-chevron${open ? ' log-chevron--open' : ''}`} aria-hidden="true">
                    <ChevronDown size={14} strokeWidth={2} />
                </span>
            </button>
            {open && <div className="faq-answer">{answer}</div>}
        </div>
    );
}

// The Help page is pure documentation: it reuses EFFECT_CARDS (the same
// established Chaos effect copy the Rule Wizard shows) so Help never drifts
// from what the app itself says.
function HelpPage() {
    return (
        <div className="help">
            <div className="help-title">HELP & FAQ</div>

            <section className="help-section">
                <h2 className="help-section-title">Getting Started</h2>
                <p className="help-lead">
                    cha0s;sim sits between your frontend and your backend. Four steps and you're seeing it work:
                </p>
                <ol className="help-steps">
                    <li className="help-step"><span className="help-step-num">1</span>Open <strong>Settings</strong> and set the Target URL to your backend (e.g. http://localhost:3000).</li>
                    <li className="help-step"><span className="help-step-num">2</span>Go to <strong>Chaos Engine</strong> or <strong>Security</strong> and hit Start — that opens a small proxy on port 8081 or 8082.</li>
                    <li className="help-step"><span className="help-step-num">3</span>Send requests through that port (e.g. http://localhost:8081/api/users) from your app, the browser, Postman, or curl.</li>
                    <li className="help-step"><span className="help-step-num">4</span>Watch the <strong>Dashboard</strong>: traffic fills the live event log, and with a rule enabled — or a suggestion badge clicked — you'll see chaos and security findings happen in real time.</li>
                </ol>
            </section>

            <section className="help-section">
                <h2 className="help-section-title">What's the difference between Chaos and Security?</h2>
                <p className="help-panel">
                    <strong>Chaos</strong> actively breaks your traffic on purpose — adding delay, faking errors, dropping connections, corrupting data — to see how your frontend handles failure.{' '}
                    <strong>Security</strong> leaves traffic alone and passively watches responses for missing security headers and leaked secrets. They run independently and can both be on at once.
                </p>
            </section>

            <section className="help-section">
                <h2 className="help-section-title">Chaos Effects Explained</h2>
                <p className="help-lead">Each rule applies one effect. These are the same descriptions the Chaos Engine uses.</p>
                <div className="help-grid">
                    {EFFECT_CARDS.map(c => {
                        const Icon = c.icon;
                        return (
                            <div className="help-card" key={c.id}>
                                <div className="help-card-head">
                                    <span className="help-card-icon"><Icon size={18} color={c.color} strokeWidth={1.8} aria-hidden="true" /></span>
                                    <span className="help-card-name">{c.name}</span>
                                </div>
                                <div className="help-card-desc">{c.desc}</div>
                            </div>
                        );
                    })}
                </div>
                <p className="help-note">
                    Frequency decides how often an enabled rule fires — from every matching request down to about 1 in 20. A rule with no frequency never fires.
                </p>
            </section>

            <section className="help-section">
                <h2 className="help-section-title">Security Checks Explained</h2>
                <p className="help-lead">The Security session watches your backend's responses for the following, without touching your traffic.</p>
                <div className="help-list">
                    <div className="help-row"><strong>Content-Security-Policy</strong><span>Controls which scripts and resources your pages are allowed to load; flagged when missing or weak.</span></div>
                    <div className="help-row"><strong>Strict-Transport-Security (HSTS)</strong><span>Forces browsers to use HTTPS only; flagged when missing or lacking max-age.</span></div>
                    <div className="help-row"><strong>X-Frame-Options</strong><span>Stops other sites embedding your page (clickjacking); must be DENY or SAMEORIGIN.</span></div>
                    <div className="help-row"><strong>X-Content-Type-Options</strong><span>Stops browsers from guessing a response's file type; must be nosniff.</span></div>
                    <div className="help-row"><strong>Leaked credentials</strong><span>Scans responses for exposed secrets — AWS access keys, private keys, JWTs, bearer tokens, API-key assignments — and flags them without ever printing the secret itself.</span></div>
                </div>
            </section>

            <section className="help-section">
                <h2 className="help-section-title">Frequently Asked Questions</h2>
                <div className="faq">
                    {HELP_FAQS.map(f => (
                        <FaqItem key={f.q} question={f.q} answer={f.a} />
                    ))}
                </div>
            </section>
        </div>
    );
}

function App() {
    const [booted, setBooted] = useState(false);
    const [fading, setFading] = useState(false);
    const [view, setView] = useState<View>('dashboard');

    const [sessionStatus, setSessionStatus] = useState<Record<string, boolean>>({});
    const [entries, setEntries] = useState<LogEntry[]>([]);
    const [targetURL, setTargetURL] = useState('');
    const [copiedId, setCopiedId] = useState<number | null>(null);
    const [now, setNow] = useState(Date.now());
    const [suggestions, setSuggestions] = useState<Record<number, SuggestionState>>({});
    const [latestStress, setLatestStress] = useState<StressUpdatePayload | null>(null);
    const [stressRunning, setStressRunning] = useState(false);
    const [stressCompletedAt, setStressCompletedAt] = useState<string | null>(null);
    const [scorecardOpen, setScorecardOpen] = useState(false);
    const [reportCopied, setReportCopied] = useState(false);
    const [logFilter, setLogFilter] = useState<'all' | 'chaos' | 'security'>('all');
    const [rulesSummary, setRulesSummary] = useState<{active: number; total: number}>({active: 0, total: 0});
    const idRef = useRef(0);
    const copyTimer = useRef<number | null>(null);
    const reportTimer = useRef<number | null>(null);

    const refresh = () => {
        GetAllSessionStatuses().then(statuses => {
            setSessionStatus(statuses);
        });
    };

    const loadRulesSummary = () => {
        GetRules().then(rules => {
            setRulesSummary({active: rules.filter(r => r.enabled).length, total: rules.length});
        });
    };

    const addEntry = (entry: Omit<LogEntry, 'id' | 'time' | 'tsMs'>) => {
        idRef.current += 1;
        const full: LogEntry = {
            ...entry,
            id: idRef.current,
            time: new Date().toLocaleTimeString(),
            tsMs: Date.now(),
        };
        setEntries(prev => [full, ...prev].slice(0, 200));
    };

    useEffect(() => {
        refresh();
        loadRulesSummary();

        // Load the upstream target URL once so the Dashboard's "Copy as cURL"
        // action can point method + path at the configured upstream target.
        GetSettings().then(s => setTargetURL(s.targetURL));

        // Poll session running states so the Dashboard overview cards and the
        // sidebar session indicators stay current while sessions run.
        const statusTimer = window.setInterval(() => {
            refresh();
            loadRulesSummary();
        }, 1000);

        // Tick frequently enough that auto-suggestion badges reliably disappear
        // ~30s after their clean traffic event arrived, without needing any
        // other state to change first.
        const nowTimer = window.setInterval(() => setNow(Date.now()), 1000);

        const offTraffic = EventsOn('traffic', (data: any) => {
            const evt = data as TrafficEventPayload;
            const sessionName = evt.sessionName || '';
            addEntry({
                severity: trafficSeverity(evt.status),
                summary: summarizeTraffic(evt),
                sessionName,
                event: evt,
            });
        });
        const offFinding = EventsOn('security_finding', (data: any) => {
            const f = data as FindingPayload;
            addEntry({
                severity: normalizeSeverity(f.FindingSeverity),
                summary: `${f.FindingCategory}: ${f.Detail}`,
                sessionName: f.sessionName || '',
                remediation: f.remediation,
                finding: f,
            });
        });
        // Keep the latest stress snapshot so the scorecard can report final
        // p50/p95/p99 latency and error rate even after a run has finished.
        // The final flag distinguishes a live run from the last completed one,
        // so the Performance section can label which one the numbers come from.
        const offStress = EventsOn('stress_update', (data: any) => {
            const u = data as StressUpdatePayload;
            setLatestStress(u);
            setStressRunning(!u.final);
            if (u.final) setStressCompletedAt(new Date().toLocaleTimeString());
        });

        return () => {
            window.clearInterval(statusTimer);
            window.clearInterval(nowTimer);
            if (copyTimer.current !== null) window.clearTimeout(copyTimer.current);
            if (reportTimer.current !== null) window.clearTimeout(reportTimer.current);
            offTraffic();
            offFinding();
            offStress();
        };
    }, []);

    // Prune suggestion feedback once its traffic entry is old enough that the
    // badges are no longer rendered, so the map can't grow without bound.
    useEffect(() => {
        const staleThreshold = Date.now() - 30000;
        setSuggestions(prev => {
            const staleIds = Object.keys(prev).filter(id => {
                const tsMs = entries.find(e => e.id === Number(id))?.tsMs ?? 0;
                return tsMs < staleThreshold;
            });
            if (staleIds.length === 0) return prev;
            const next = {...prev};
            staleIds.forEach(id => delete next[Number(id)]);
            return next;
        });
    }, [entries, now]);

    const handleCopy = (entryId: number, evt: TrafficEventPayload) => {
        const curl = generateCurl(evt, targetURL);
        ClipboardSetText(`${CURL_DISCLOSURE}\n${curl}`).then(() => {
            setCopiedId(entryId);
            if (copyTimer.current !== null) window.clearTimeout(copyTimer.current);
            copyTimer.current = window.setTimeout(() => setCopiedId(null), 1500);
        });
    };

    // Copy the Markdown export of the current scorecard to the clipboard and
    // flash an inline "Copied!" state on the button. The export carries the
    // same scope/attribution/stress-status disclosures as the on-screen modal.
    const handleCopyReport = (stats: ScorecardStats) => {
        const ctx: ReportContext = {
            stressRunning,
            stressCompletedAt,
        };
        ClipboardSetText(buildMarkdownReport(stats, ctx)).then(() => {
            setReportCopied(true);
            if (reportTimer.current !== null) window.clearTimeout(reportTimer.current);
            reportTimer.current = window.setTimeout(() => setReportCopied(false), 1500);
        });
    };

    // One-click chaos injection from a clean traffic event. Builds a fully
    // pre-filled CreateRuleRequest for the exact path+method and fires it
    // directly through the backend binding (the same channel the wizard uses),
    // then surfaces success/failure inline on the badge.
    const applySuggestion = async (entryId: number, evt: TrafficEventPayload, action: SuggestionAction) => {
        const mark = (status: SuggestionState['latency']['status'], message?: string) => {
            setSuggestions(prev => {
                const existing = prev[entryId] ?? {latency: {status: 'idle'}, error: {status: 'idle'}};
                return {...prev, [entryId]: {...existing, [action]: {status, message}}};
            });
        };

        const method = (evt.method || 'GET').toUpperCase();
        const label = action === 'latency' ? '+3s latency' : '+500 error';
        const baseName = `${method} ${evt.path} — ${label}`;
        mark('creating');

        // Rule names must be unique, and this name is deterministic for a given
        // method+path+action. Before creating, check the current rule list and
        // append the lowest unused "(N)" suffix so clicking the same suggestion
        // twice creates a distinct rule instead of a duplicate-name failure.
        const existingNames = new Set((await GetRules()).map(r => r.name));
        let name = baseName;
        for (let n = 2; existingNames.has(name); n++) {
            name = `${baseName} (${n})`;
        }

        const req = new config.CreateRuleRequest({
            name,
            path: evt.path,
            methods: [method],
            frequency: 'always',
            effect: action === 'latency' ? 'slow' : 'error',
            slowMs: action === 'latency' ? 3000 : undefined,
            errorCode: action === 'error' ? 500 : undefined,
        });
        CreateRule(req).then(result => {
            if (result === '') {
                mark('added', 'Added');
                return;
            }
            if (result.startsWith('warning:')) {
                // Rule created in-memory but not persisted; treat as added.
                mark('added', 'Added');
                return;
            }
            mark('error', result);
        });
    };

    const handleBoot = () => {
        if (fading) return;
        setFading(true);
        window.setTimeout(() => setBooted(true), 350);
    };

    const typedWordmark = useTypewriter(BOOT_WORDMARK, 160);

    if (!booted) {
        return (
            <div
                className={`boot-screen${fading ? ' boot-screen--fading' : ''}`}
                style={{backgroundImage: `url(${bootBackground})`}}
                onClick={handleBoot}
            >
                <div className="wordmark wordmark--boot">
                    {typedWordmark}<span className="cursor">|</span>
                </div>
            </div>
        );
    }

    return (
        <div className="app-layout">
            <aside className="sidebar">
                <div className="wordmark wordmark--sidebar">cha0s;sim</div>
                <nav className="nav">
                    <button
                        className={`nav-item${view === 'dashboard' ? ' nav-item--active' : ''}`}
                        onClick={() => setView('dashboard')}
                    >DASHBOARD</button>
                    <button
                        className={`nav-item${view === 'chaos' ? ' nav-item--active' : ''}`}
                        onClick={() => setView('chaos')}
                    >CHAOS ENGINE</button>
                    <button
                        className={`nav-item${view === 'security' ? ' nav-item--active' : ''}`}
                        onClick={() => setView('security')}
                    >SECURITY</button>
                    <button
                        className={`nav-item${view === 'stress' ? ' nav-item--active' : ''}`}
                        onClick={() => setView('stress')}
                    >STRESS TEST</button>
                    <button
                        className={`nav-item${view === 'settings' ? ' nav-item--active' : ''}`}
                        onClick={() => {
                            setView('settings');
                            refresh();
                        }}
                    >SETTINGS</button>
                    <button
                        className={`nav-item${view === 'help' ? ' nav-item--active' : ''}`}
                        onClick={() => setView('help')}
                    >HELP</button>
                </nav>
                <div className="session-indicators">
                    {SESSION_OVERVIEW.map(session => {
                        const isRunning = !!sessionStatus[session.name];
                        return (
                            <button
                                key={session.name}
                                className="session-indicator"
                                onClick={() => setView(session.view)}
                                title={`${session.label} — port ${session.port} (${isRunning ? 'running' : 'stopped'})`}
                            >
                                <span className={`status-dot${isRunning ? ' status-dot--running' : ''}`} />
                                <span>{session.label}</span>
                            </button>
                        );
                    })}
                </div>
            </aside>
            <main className="main">
                <header className="topbar">
                    <div className="topbar-title">
                        {view === 'dashboard' && 'DASHBOARD'}
                        {view === 'chaos' && 'CHAOS ENGINE'}
                        {view === 'security' && 'SECURITY'}
                        {view === 'stress' && 'STRESS TEST'}
                        {view === 'settings' && 'SETTINGS'}
                        {view === 'help' && 'HELP'}
                    </div>
                </header>

                {view === 'dashboard' && (
                    <div className="dashboard">
                        <div className="kpi-strip">
                            {SESSION_OVERVIEW.map(session => {
                                const isRunning = !!sessionStatus[session.name];
                                const isChaos = session.name === CHAOS_SESSION_NAME;
                                return (
                                    <button
                                        key={session.name}
                                        className={`kpi-card kpi-card--button${isChaos ? ' kpi-card--chaos' : ' kpi-card--security'}`}
                                        onClick={() => setView(session.view)}
                                    >
                                        <div className="kpi-head">
                                            <span className="kpi-icon">
                                                {isChaos
                                                    ? <Boxes size={15} strokeWidth={2.2} aria-hidden="true" />
                                                    : <Shield size={15} strokeWidth={2.2} aria-hidden="true" />}
                                            </span>
                                            <span className="kpi-label">{session.label} Session</span>
                                            <ArrowRight className="kpi-arrow" size={14} strokeWidth={2.2} aria-hidden="true" />
                                        </div>
                                        <div className="kpi-value-row">
                                            <span className={`status-dot${isRunning ? ' status-dot--running' : ''}`} />
                                            <span className={`kpi-value${isRunning ? ' kpi-value--running' : ''}`}>
                                                {isRunning ? 'Running' : 'Stopped'}
                                            </span>
                                        </div>
                                        <div className="kpi-detail">port {session.port}</div>
                                    </button>
                                );
                            })}
                            {(() => {
                                const trafficCount = entries.filter(e => e.event).length;
                                const findings = entries.filter(e => e.finding);
                                const criticalFindings = findings.filter(e => e.severity === 'critical').length;
                                const hasCritical = criticalFindings > 0;
                                return (
                                    <>
                                        <div className="kpi-card">
                                            <div className="kpi-head">
                                                <span className="kpi-icon"><Activity size={15} strokeWidth={2.2} aria-hidden="true" /></span>
                                                <span className="kpi-label">Requests Seen</span>
                                            </div>
                                            <div className="kpi-value">{trafficCount}</div>
                                            <div className="kpi-detail">traffic in live buffer</div>
                                        </div>
                                        <div className={`kpi-card${hasCritical ? ' kpi-card--danger' : findings.length > 0 ? ' kpi-card--good' : ''}`}>
                                            <div className="kpi-head">
                                                <span className="kpi-icon"><ShieldAlert size={15} strokeWidth={2.2} aria-hidden="true" /></span>
                                                <span className="kpi-label">Findings</span>
                                            </div>
                                            <div className={`kpi-value${hasCritical ? ' kpi-value--critical' : findings.length > 0 ? ' kpi-value--good' : ''}`}>
                                                {findings.length}
                                            </div>
                                            <div className="kpi-detail">{hasCritical ? `${criticalFindings} critical` : 'all clear'}</div>
                                        </div>
                                        <div className="kpi-card">
                                            <div className="kpi-head">
                                                <span className="kpi-icon"><SlidersHorizontal size={15} strokeWidth={2.2} aria-hidden="true" /></span>
                                                <span className="kpi-label">Active Rules</span>
                                            </div>
                                            <div className="kpi-value kpi-value--accent">{rulesSummary.active}</div>
                                            <div className="kpi-detail">{rulesSummary.total} total rule{rulesSummary.total === 1 ? '' : 's'}</div>
                                        </div>
                                    </>
                                );
                            })()}
                        </div>
                        <div className="log-wrap">
                            <div className="log-title-row">
                                <div className="log-title">LIVE EVENT LOG — traffic + security_finding</div>
                                <div className="log-title-actions">
                                    <div className="log-filter">
                                        {(['all', 'chaos', 'security'] as const).map(f => (
                                            <button
                                                key={f}
                                                className={`log-filter-btn${logFilter === f ? ' log-filter-btn--active' : ''}${f !== 'all' ? ` log-filter-btn--${f}` : ''}`}
                                                onClick={() => setLogFilter(f)}
                                            >
                                                {f === 'all' ? 'All' : f.toUpperCase()}
                                            </button>
                                        ))}
                                    </div>
                                    <button className="btn scorecard-trigger" onClick={() => setScorecardOpen(true)}>
                                        Generate Report
                                    </button>
                                </div>
                            </div>
                            <div className="log-panel">
                                {entries.length === 0 && (
                                    <div className="log-empty">Awaiting traffic... start a session and send a request.</div>
                                )}
                                {entries.length > 0 && logFilter !== 'all' && !entries.some(e => e.sessionName === logFilter) && (
                                    <div className="log-empty">
                                        No {logFilter} events recorded yet. Start that session and send some traffic.
                                    </div>
                                )}
                                {entries.filter(e => logFilter === 'all' || e.sessionName === logFilter).map(e => {
                                    if (e.finding) {
                                        return <FindingLogRow entry={e} showSession key={e.id} />;
                                    }
                                    const evt = e.event;
                                    const isCopied = copiedId === e.id;
                                    // Suggest chaos only for clean traffic events
                                    // that are still recent enough to be useful.
                                    const isClean = !!evt && (!evt.effects || evt.effects.length === 0);
                                    const isFresh = now - e.tsMs < 30000;
                                    const suggest = isClean && isFresh;
                                    const sug = suggestions[e.id];
                                    return (
                                        <div className="log-entry" key={e.id}>
                                            <span className="log-time">{e.time}</span>
                                            {e.sessionName && (
                                                <span className={`log-session log-session--${e.sessionName}`}>
                                                    {e.sessionName === CHAOS_SESSION_NAME ? 'CHAOS' : 'SECURITY'}
                                                </span>
                                            )}
                                            <span className={`log-dot log-dot--${e.severity}`} />
                                            <span className="log-summary">{e.summary}</span>
                                            {suggest && evt && (
                                                <span className="log-suggestions">
                                                    <SuggestionBadge
                                                        label="+3s Latency"
                                                        icon={Zap}
                                                        state={sug?.latency ?? {status: 'idle'}}
                                                        onClick={() => applySuggestion(e.id, evt, 'latency')}
                                                    />
                                                    <SuggestionBadge
                                                        label="+500 Error"
                                                        icon={Flame}
                                                        state={sug?.error ?? {status: 'idle'}}
                                                        onClick={() => applySuggestion(e.id, evt, 'error')}
                                                    />
                                                </span>
                                            )}
                                            {evt ? (
                                                <button
                                                    className={`log-copy${isCopied ? ' log-copy--copied' : ''}`}
                                                    onClick={() => handleCopy(e.id, evt)}
                                                    title="Copy method + URL only — headers, body, and query string are not captured"
                                                    aria-label="Copy method + URL (headers, body, query not captured)"
                                                >
                                                    {isCopied ? 'Copied!' : '⎘'}
                                                </button>
                                            ) : null}
                                        </div>
                                    );
                                })}
                            </div>
                        </div>
                    </div>
                )}

                {view === 'chaos' && (
                    <ChaosEngine entries={entries} now={now} />
                )}

                {view === 'security' && (
                    <SecurityView />
                )}

                {view === 'stress' && (
                    <StressTest />
                )}

                {view === 'settings' && (
                    <SettingsForm />
                )}

                {view === 'help' && (
                    <HelpPage />
                )}
            </main>
            {scorecardOpen && (
                <ScorecardModal
                    stats={computeScorecard(entries, latestStress)}
                    onClose={() => setScorecardOpen(false)}
                    onCopy={() => handleCopyReport(computeScorecard(entries, latestStress))}
                    copied={reportCopied}
                    stressRunning={stressRunning}
                    stressCompletedAt={stressCompletedAt}
                />
            )}
        </div>
    );
}

export default App