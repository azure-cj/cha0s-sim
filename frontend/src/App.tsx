import {useEffect, useRef, useState} from 'react';
import {EventsOn} from "../wailsjs/runtime/runtime";
import {GetStatus, GetRuleCount, StartProxy, StopProxy} from "../wailsjs/go/main/App";
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
                        onClick={() => setView('settings')}
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
                    <div className="placeholder">
                        <h2>CHAOS ENGINE</h2>
                        <p>Coming soon — rule management and chaos configuration will live here.</p>
                    </div>
                )}

                {view === 'settings' && (
                    <div className="placeholder">
                        <h2>SETTINGS</h2>
                        <p>Coming soon — proxy target, port, and config settings will live here.</p>
                    </div>
                )}
            </main>
        </div>
    );
}

export default App