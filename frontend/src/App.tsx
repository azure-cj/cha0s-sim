import {ChangeEvent, useCallback, useEffect, useRef, useState} from 'react';
import {EventsOn} from "../wailsjs/runtime/runtime";
import {
    CreateRule,
    GetAllSessionStatuses,
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
import {config, main} from '../wailsjs/go/models';
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

type View = 'dashboard' | 'chaos' | 'stress' | 'security' | 'settings';

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

function ChaosEngine() {
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
            {wizardOpen && <RuleCreateWizard onClose={() => setWizardOpen(false)} onCreated={handleCreated} />}
        </div>
    );
}

// ---------- create-rule wizard ----------

type EffectId = 'slow' | 'error' | 'drop' | 'corrupt' | 'attack';
type AttackId = 'sqli' | 'xss' | 'path_traversal';

const EFFECT_CARDS: {id: EffectId; icon: string; name: string; desc: string}[] = [
    {id: 'slow', icon: '🐢', name: 'Slow it down', desc: 'Delay the response to simulate a slow network'},
    {id: 'error', icon: '⚠️', name: 'Fake an error', desc: 'Return a fake error status instead of the real response'},
    {id: 'drop', icon: '🔌', name: 'Cut the connection', desc: 'Simulate the connection suddenly dropping'},
    {id: 'corrupt', icon: '🧬', name: 'Corrupt the data', desc: 'Remove fields from the response'},
    {id: 'attack', icon: '🎯', name: 'Test for security holes', desc: 'Send malicious test data to check input validation'},
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
                            <input
                                className="settings-input"
                                id="wizard-path"
                                type="text"
                                placeholder="e.g. /api/checkout"
                                value={path}
                                onChange={e => setPath(e.target.value)}
                            />
                            <span className="wizard-note">
                                This is the URL path your app calls, like /api/users or /api/checkout.
                            </span>
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
                            {EFFECT_CARDS.map(c => (
                                <button
                                    type="button"
                                    key={c.id}
                                    className={`stress-preset wizard-card${effect === c.id ? ' wizard-card--active' : ''}`}
                                    onClick={() => setEffect(c.id)}
                                >
                                    <span className="stress-preset-icon">{c.icon}</span>
                                    <span className="stress-preset-name">{c.name}</span>
                                    <span className="stress-preset-desc">{c.desc}</span>
                                </button>
                            ))}
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
                                        <span className="chaos-tag" key={t.kind} title={t.title}>{t.label}</span>
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
                severity: normalizeSeverity(f.FindingSeverity),
                summary: `${f.FindingCategory}: ${f.Detail}`,
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
                        <div className="log-entry" key={e.id}>
                            <span className="log-time">{e.time}</span>
                            <span className={`log-dot log-dot--${e.severity}`} />
                            <span className="log-summary">{e.summary}</span>
                        </div>
                    ))}
                </div>
            </div>
        </div>
    );
}

function App() {
    const [booted, setBooted] = useState(false);
    const [fading, setFading] = useState(false);
    const [view, setView] = useState<View>('dashboard');

    const [sessionStatus, setSessionStatus] = useState<Record<string, boolean>>({});
    const [entries, setEntries] = useState<LogEntry[]>([]);
    const idRef = useRef(0);

    const refresh = () => {
        GetAllSessionStatuses().then(setSessionStatus);
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

        // Poll session running states so the Dashboard overview cards and the
        // sidebar session indicators stay current while sessions run.
        const statusTimer = window.setInterval(refresh, 1000);

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
            window.clearInterval(statusTimer);
            offTraffic();
            offFinding();
        };
    }, []);

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
                    </div>
                </header>

                {view === 'dashboard' && (
                    <div className="dashboard">
                        <div className="overview">
                            {SESSION_OVERVIEW.map(session => {
                                const isRunning = !!sessionStatus[session.name];
                                return (
                                    <button
                                        key={session.name}
                                        className="overview-card"
                                        onClick={() => setView(session.view)}
                                    >
                                        <div className="overview-card-header">
                                            <span className={`status-dot${isRunning ? ' status-dot--running' : ''}`} />
                                            <span className={`overview-status${isRunning ? ' overview-status--running' : ''}`}>
                                                {isRunning ? 'Running' : 'Stopped'}
                                            </span>
                                        </div>
                                        <div className="overview-name">{session.label}</div>
                                        <div className="overview-port">port {session.port}</div>
                                        <div className="overview-hint">Open {session.label} →</div>
                                    </button>
                                );
                            })}
                        </div>
                        <div className="log-wrap">
                            <div className="log-title">LIVE EVENT LOG — traffic + security_finding</div>
                            <div className="log-panel">
                                {entries.length === 0 && (
                                    <div className="log-empty">Awaiting traffic... start a session and send a request.</div>
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
                    </div>
                )}

                {view === 'chaos' && (
                    <ChaosEngine />
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
            </main>
        </div>
    );
}

export default App