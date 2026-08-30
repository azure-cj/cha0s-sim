import {useCallback, useEffect, useRef, useState} from 'react';
import {EventsOn} from "../wailsjs/runtime/runtime";
import {GetRules, GetRuleCount, GetSettings, GetStatus, SaveSettings, StartProxy, StopProxy, ToggleRule} from "../wailsjs/go/main/App";
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

interface LogEntry {
    id: number;
    time: string;
    severity: Severity;
    summary: string;
}

type View = 'dashboard' | 'chaos' | 'settings';

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

function chaosPathLabel(r: main.RuleView): string {
    if (r.pathRegex) return `regex: ${r.pathRegex}`;
    return r.path || 'ANY';
}

function chaosMethodsLabel(r: main.RuleView): string {
    if (!r.methods || r.methods.length === 0) return 'ALL';
    return r.methods.join(', ');
}

function chaosErrorRateLabel(r: main.RuleView): string {
    return `${Math.round((r.errorRate || 0) * 100)}%`;
}

function chaosTags(r: main.RuleView): string[] {
    const tags: string[] = [];
    if (r.hasLatency) tags.push('Latency');
    if (r.hasStatusOverride) tags.push('Status Override');
    if (r.hasDropConnection) tags.push('Drop');
    if (r.hasMangle) tags.push('Mangle');
    if (r.hasFuzz) tags.push('Fuzz');
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
    const [pending, setPending] = useState<string | null>(null);

    const loadRules = useCallback(() => {
        GetRules().then(setRules);
    }, []);

    useEffect(() => {
        loadRules();
    }, [loadRules]);

    const toggle = async (rule: main.RuleView, newEnabled: boolean) => {
        if (pending !== null) return;
        setPending(rule.name);
        setErrors(prev => {
            if (!prev[rule.name]) return prev;
            const next = {...prev};
            delete next[rule.name];
            return next;
        });
        setRules(prev => prev.map(r => (r.name === rule.name ? {...r, enabled: newEnabled} : r)));
        const err = await ToggleRule(rule.name, newEnabled);
        setPending(null);
        if (err) {
            setRules(prev => prev.map(r => (r.name === rule.name ? {...r, enabled: !newEnabled} : r)));
            setErrors(prev => ({...prev, [rule.name]: err}));
            return;
        }
        loadRules();
    };

    return (
        <div className="chaos">
            <div className="chaos-title">CHAOS RULES</div>
            <div className="chaos-disclaimer">
                Toggling rules here is temporary — changes apply immediately to live traffic but are not saved to your
                config file. Editing chaos.yaml directly will override any toggles made here.
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
                    const tags = chaosTags(r);
                    return (
                        <div className="chaos-row" key={r.name}>
                            <div className="chaos-row-info">
                                <div className="chaos-row-name">{r.name}</div>
                                <div className="chaos-row-meta">
                                    <span>{chaosPathLabel(r)}</span>
                                    <span>Methods: {chaosMethodsLabel(r)}</span>
                                    <span>Error Rate: {chaosErrorRateLabel(r)}</span>
                                </div>
                                {tags.length > 0 && (
                                    <div className="chaos-row-tags">
                                        {tags.map(t => (
                                            <span className="chaos-tag" key={t}>{t}</span>
                                        ))}
                                    </div>
                                )}
                                {err && <div className="chaos-row-error">{err}</div>}
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

                {view === 'settings' && (
                    <SettingsForm running={running} />
                )}
            </main>
        </div>
    );
}

export default App