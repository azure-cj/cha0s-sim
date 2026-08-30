import {ChangeEvent, useCallback, useEffect, useRef, useState} from 'react';
import {EventsOn} from "../wailsjs/runtime/runtime";
import {
    GetRules,
    GetRuleCount,
    GetSettings,
    GetStatus,
    GetStressStatus,
    SaveSettings,
    StartProxy,
    StartStressTest,
    StopProxy,
    StopStressTest,
    ToggleRule,
} from "../wailsjs/go/main/App";
import {main} from '../wailsjs/go/models';
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
    effects?: {kind: string; detail: string; rule: string}[];
}

interface FindingPayload {
    FindingCategory: string;
    Detail: string;
    FindingSeverity: string;
    Location: string;
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
}

interface LogEntry {
    id: number;
    time: string;
    severity: Severity;
    summary: string;
}

type View = 'dashboard' | 'chaos' | 'stress' | 'settings';

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

interface ChaosTag {
    kind: string;
    label: string; // emoji + short plain-language label shown in the pill
    title: string; // HTML title tooltip with a one-line plain-language description
}

function chaosTags(r: main.RuleView): ChaosTag[] {
    const tags: ChaosTag[] = [];
    if (r.hasLatency) tags.push({kind: 'latency', label: '🐢 Slows it down', title: 'Delays the response to simulate a slow network'});
    if (r.hasStatusOverride) tags.push({kind: 'status', label: '⚠️ Fakes an error', title: 'Returns a fake error status like 500 or 503'});
    if (r.hasDropConnection) tags.push({kind: 'drop', label: '🔌 Cuts the connection', title: 'Simulates the connection suddenly dropping, like lost WiFi'});
    if (r.hasMangle) tags.push({kind: 'mangle', label: '🧬 Corrupts the data', title: 'Deletes or corrupts fields in the response'});
    if (r.hasFuzz) tags.push({kind: 'fuzz', label: '🎯 Injects bad input', title: 'Sends malicious test payloads like SQL injection or XSS strings'});
    return tags;
}

function SettingsForm({running}: {running: boolean}) {
    const [targetURL, setTargetURL] = useState('');
    const [proxyPort, setProxyPort] = useState('');
    const [configPath, setConfigPath] = useState('');
    const [firstRun, setFirstRun] = useState(false);
    const [error, setError] = useState('');
    const [success, setSuccess] = useState(false);
    const successTimer = useRef<number | null>(null);

    const applyFromServer = (s: main.SettingsView) => {
        setTargetURL(s.targetURL);
        setProxyPort(String(s.proxyPort));
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
        const err = await SaveSettings(targetURL, Number(proxyPort), configPath);
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

            {running && (
                <div className="settings-warn">
                    Stop the proxy from the Dashboard to change settings.
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
                        disabled={running}
                    />
                </div>

                <div className="settings-field">
                    <label htmlFor="settings-port">Proxy Port</label>
                    <input
                        className="settings-input"
                        id="settings-port"
                        type="number"
                        min={1}
                        max={65535}
                        placeholder="8080"
                        value={proxyPort}
                        onChange={e => setProxyPort(e.target.value)}
                        disabled={running}
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
                        disabled={running}
                    />
                </div>

                <div className="settings-actions">
                    <button className="btn" type="submit" disabled={running}>
                        Save Settings
                    </button>
                    {error && <span className="settings-error">{error}</span>}
                    {success && <span className="settings-success">Settings saved</span>}
                </div>
            </form>
        </div>
    );
}

function ChaosEngine() {
    const [rules, setRules] = useState<main.RuleView[]>([]);
    const [errors, setErrors] = useState<Record<string, string>>({});
    const [warnings, setWarnings] = useState<Record<string, string>>({});
    const [pending, setPending] = useState<string | null>(null);
    const warningTimers = useRef<Record<string, number>>({});

    const loadRules = useCallback(() => {
        GetRules().then(setRules);
    }, []);

    useEffect(() => {
        loadRules();
        return () => {
            Object.values(warningTimers.current).forEach(id => window.clearTimeout(id));
            warningTimers.current = {};
        };
    }, [loadRules]);

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
            <div className="chaos-summary">
                You have {rules.length} {ruleWord} configured. {activeCount} {activeWord} currently active.
            </div>
            <div className="chaos-disclaimer">
                Turning a rule on/off here works right away, but won't be remembered if you edit your config file
                directly.
            </div>
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
                                            <span className="chaos-tag" key={t.kind} title={t.title}>{t.label}</span>
                                        ))}
                                    </div>
                                )}
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

interface PresetDef {
    id: PresetId;
    icon: string;
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
        {id: 'light', icon: '🟢', name: 'Light Load', desc: 'Steady 50 RPS for 30 seconds'},
        {id: 'viral', icon: '🟡', name: 'Viral Spike', desc: 'Baseline 20 RPS — spikes to 500 RPS immediately for the first 5s of a 20s run, then settles back down'},
        {id: 'stressbreak', icon: '🔴', name: 'Stress & Break', desc: 'Ramps up traffic until your backend starts struggling — auto-stops at a 50% error rate'},
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
                    {presets.map(p => (
                        <button className="stress-preset" key={p.id} onClick={() => launchPreset(p.id)}>
                            <span className="stress-preset-icon">{p.icon}</span>
                            <span className="stress-preset-name">{p.name}</span>
                            <span className="stress-preset-desc">{p.desc}</span>
                        </button>
                    ))}
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
    const maxRps = Math.max(...history.map(h => h.rps), 1);
    const rpsPct = rpsVal > 0 ? Math.max(3, Math.min(100, (rpsVal / maxRps) * 100)) : 0;
    const errPct = Math.min(100, errorRate * 100);

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
                <div className="stress-feed-row">
                    <span className="stress-feed-label">RPS</span>
                    <div className="stress-feed-track">
                        <div className="stress-feed-bar stress-feed-bar--rps" style={{width: `${rpsPct}%`}} />
                    </div>
                    <span className="stress-feed-value">{Math.round(rpsVal)}</span>
                </div>
                <div className="stress-feed-row">
                    <span className="stress-feed-label">ERRORS</span>
                    <div className="stress-feed-track">
                        <div className={`stress-feed-bar stress-feed-bar--${tone}`} style={{width: `${errPct}%`}} />
                    </div>
                    <span className="stress-feed-value">{Math.round(errorRate * 100)}%</span>
                </div>
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

function App() {
    const [booted, setBooted] = useState(false);
    const [fading, setFading] = useState(false);
    const [view, setView] = useState<View>('dashboard');

    const [status, setStatus] = useState('');
    const [ruleCount, setRuleCount] = useState(0);
    const [entries, setEntries] = useState<LogEntry[]>([]);
    const idRef = useRef(0);

    const running = status.toLowerCase().startsWith('running');

    const refresh = () => {
        GetStatus().then(setStatus);
        GetRuleCount().then(setRuleCount);
    };

    const addEntry = (entry: Omit<LogEntry, 'id' | 'time'>) => {
        idRef.current += 1;
        const full: LogEntry = {
            ...entry,
            id: idRef.current,
            time: new Date().toLocaleTimeString(),
        };
        setEntries(prev => [full, ...prev].slice(0, 200));
    };

    useEffect(() => {
        refresh();

        const offTraffic = EventsOn('traffic', (data: any) => {
            const evt = data as TrafficEventPayload;
            addEntry({
                severity: trafficSeverity(evt.status),
                summary: summarizeTraffic(evt),
            });
        });
        const offFinding = EventsOn('security_finding', (data: any) => {
            const f = data as FindingPayload;
            addEntry({
                severity: normalizeSeverity(f.FindingSeverity),
                summary: `${f.FindingCategory}: ${f.Detail}`,
            });
        });

        return () => {
            offTraffic();
            offFinding();
        };
    }, []);

    const start = () => {
        StartProxy().then(err => {
            setStatus(err !== "" ? err : "starting...");
            refresh();
        });
    };

    const stop = () => {
        StopProxy().then(err => {
            setStatus(err !== "" ? err : "stopping...");
            refresh();
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
                </nav>
                <div className="status-badge">
                    <span className={`status-dot${running ? ' status-dot--running' : ''}`} />
                    <span>{running ? 'Running' : 'Stopped'}</span>
                    <span className="status-rules">{ruleCount} rules</span>
                </div>
            </aside>
            <main className="main">
                <header className="topbar">
                    <button className="btn btn--primary" onClick={start}>Start Proxy</button>
                    <button className="btn btn--danger" onClick={stop}>Stop Proxy</button>
                </header>

                {view === 'dashboard' && (
                    <div className="log-wrap">
                        <div className="log-title">LIVE EVENT LOG — traffic + security_finding</div>
                        <div className="log-panel">
                            {entries.length === 0 && (
                                <div className="log-empty">Awaiting traffic... start the proxy and send a request.</div>
                            )}
                            {entries.map(e => (
                                <div className="log-entry" key={e.id}>
                                    <span className="log-time">{e.time}</span>
                                    <span className={`log-dot log-dot--${e.severity}`} />
                                    <span className="log-summary">{e.summary}</span>
                                </div>
                            ))}
                        </div>
                    </div>
                )}

                {view === 'chaos' && (
                    <ChaosEngine />
                )}

                {view === 'stress' && (
                    <StressTest />
                )}

                {view === 'settings' && (
                    <SettingsForm running={running} />
                )}
            </main>
        </div>
    );
}

export default App