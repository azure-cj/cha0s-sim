import {useCallback, useEffect, useRef, useState} from 'react';
import {Activity, Boxes, ChevronDown, Crosshair} from 'lucide-react';

// Mirrors of the App types kept local so App.tsx only needs to import the
// component and render it. Structural typing makes the App's LogEntry[] assign.
const CHAOS_SESSION_PORT = 8081;

interface FlowEffect {
    kind: string;
    detail: string;
    rule: string;
    delay_ms?: number;
}

interface FlowEvent {
    status: number;
    effects?: FlowEffect[];
}

interface FlowEntry {
    id: number;
    sessionName: string;
    event?: FlowEvent;
}

// passthrough = neutral colour; every other outcome maps to an explicit visual.
type FlowOutcome = 'passthrough' | 'latency' | 'override' | 'mangle' | 'fuzz' | 'drop';

interface FlowClassification {
    outcome: FlowOutcome;
    detail: string | null; // latency Detail string, shown beside the proxy
    statusLabel: string | null; // override status, shown at the Backend end
    pauseMs: number; // proxy hold for this particle, 0 unless latency
}

interface FlowParticle extends FlowClassification {
    id: number;
    startTs: number;
    lifetime: number;
}

const FLOW_PARTICLE_CAP = 30; // hard ceiling on simultaneous particles
const FLOW_SEGMENT_MS = 520; // travel time for each half of the journey
const FLOW_LATENCY_PAUSE_MS = 600; // fallback proxy hold when no applied delay is reported
const FLOW_LATENCY_MAX_MS = 2000; // display cap for reconstructed latency
const FLOW_DROP_FADE_MS = 420; // fade-out once a dropped dot stops at the proxy
const FLOW_RENDER_INTERVAL_MS = 1000 / 30; // cap renders at roughly 30fps

const FLOW_X_APP = 8;
const FLOW_X_PROXY = 50;
const FLOW_X_BACKEND = 92;

function isChaosTraffic(sessionName: string): boolean {
    return sessionName === 'chaos' || sessionName === '';
}

// Maps a completed traffic event to a single visual outcome. Precedence mirrors
// how overlapping effects are reported on one request: a dropped connection
// dominates everything, then a status override, then mangle, then fuzz, then
// latency; anything else is a plain pass-through. Pure: no rendering, no side
// effects.
function classifyEvent(evt: FlowEvent): FlowClassification {
    const effects = evt.effects ?? [];
    const kinds = new Set(effects.map(e => e.kind));
    if (kinds.has('drop') || evt.status === 0) {
        return {outcome: 'drop', detail: null, statusLabel: null, pauseMs: 0};
    }
    if (kinds.has('override')) {
        return {outcome: 'override', detail: null, statusLabel: evt.status ? `HTTP ${evt.status}` : null, pauseMs: 0};
    }
    if (kinds.has('mangle')) {
        return {outcome: 'mangle', detail: null, statusLabel: null, pauseMs: 0};
    }
    if (kinds.has('fuzz')) {
        const fuzz = effects.find(e => e.kind === 'fuzz');
        // Fuzz outranks latency visually, but a latency effect on the same
        // request still holds the particle at the proxy for its real pause.
        return {outcome: 'fuzz', detail: fuzz ? fuzz.detail : null, statusLabel: null, pauseMs: kinds.has('latency') ? latencyPauseMs(effects) : 0};
    }
    if (kinds.has('latency')) {
        const latency = effects.find(e => e.kind === 'latency');
        return {outcome: 'latency', detail: latency ? latency.detail : null, statusLabel: null, pauseMs: latencyPauseMs(effects)};
    }
    return {outcome: 'passthrough', detail: null, statusLabel: null, pauseMs: 0};
}

// Proxy hold for a latency event, taken from the delay the proxy actually
// applied. Falls back to the illustrative pause when no applied delay is
// reported, sums when several latency effects fire on one request, and caps for
// display so a very slow rule does not stall the animation.
function latencyPauseMs(effects: FlowEffect[]): number {
    let total = 0;
    for (const e of effects) {
        if (e.kind !== 'latency') continue;
        total += typeof e.delay_ms === 'number' && e.delay_ms > 0 ? e.delay_ms : FLOW_LATENCY_PAUSE_MS;
    }
    return Math.min(total, FLOW_LATENCY_MAX_MS);
}

function flowLifetimeMs(classification: FlowClassification): number {
    if (classification.outcome === 'drop') return FLOW_SEGMENT_MS + FLOW_DROP_FADE_MS;
    return FLOW_SEGMENT_MS * 2 + classification.pauseMs;
}

function lerp(a: number, b: number, t: number): number {
    const clamped = t < 0 ? 0 : t > 1 ? 1 : t;
    return a + (b - a) * clamped;
}

// Horizontal position (as a percentage of the track) at a given elapsed time.
function flowX(p: FlowParticle, elapsed: number): number {
    if (elapsed <= FLOW_SEGMENT_MS) {
        return lerp(FLOW_X_APP, FLOW_X_PROXY, elapsed / FLOW_SEGMENT_MS);
    }
    if (p.outcome === 'drop') return FLOW_X_PROXY; // stops dead at the proxy
    const afterSeg = elapsed - FLOW_SEGMENT_MS;
    if (afterSeg <= p.pauseMs) return FLOW_X_PROXY;
    return lerp(FLOW_X_PROXY, FLOW_X_BACKEND, (afterSeg - p.pauseMs) / FLOW_SEGMENT_MS);
}

// ChaosFlow animates chaos-session traffic through a three-node diagram. It only
// reacts to events that arrive AFTER it mounts: the first effect run records the
// current end of the buffer as a baseline, so opening this view never replays
// old traffic. Motion is driven by a single requestAnimationFrame loop that runs
// only while particles exist; particle state lives in a ref and React re-renders
// at a capped rate (not once per particle per frame).
export function ChaosFlow({entries, running}: {entries: FlowEntry[]; running: boolean}) {
    const [open, setOpen] = useState(true);
    const [pulse, setPulse] = useState<{rule: string; nonce: number} | null>(null);
    const [reduceMotion, setReduceMotion] = useState(
        () => typeof window !== 'undefined' && window.matchMedia('(prefers-reduced-motion: reduce)').matches
    );

    const particlesRef = useRef<FlowParticle[]>([]);
    const lastSeenIdRef = useRef<number | null>(null);
    const particleIdRef = useRef(0);
    const rafRef = useRef<number | null>(null);
    const lastRenderRef = useRef(0);
    const pulseTimer = useRef<number | null>(null);
    // Read inside the spawn effect without becoming dependencies: a change to
    // motion preference or panel state must not itself re-trigger spawning.
    const reduceMotionRef = useRef(reduceMotion);
    const openRef = useRef(open);
    const [, setFrame] = useState(0);

    useEffect(() => {
        openRef.current = open;
    }, [open]);

    useEffect(() => {
        reduceMotionRef.current = reduceMotion;
        if (reduceMotion) {
            // Turning motion off un-renders in-flight particles, so clear them
            // and stop the loop rather than leaving a running animation.
            particlesRef.current = [];
            if (rafRef.current !== null) {
                cancelAnimationFrame(rafRef.current);
                rafRef.current = null;
            }
            setFrame(f => f + 1);
        }
    }, [reduceMotion]);

    useEffect(() => {
        const mq = window.matchMedia('(prefers-reduced-motion: reduce)');
        const onChange = () => setReduceMotion(mq.matches);
        mq.addEventListener('change', onChange);
        return () => mq.removeEventListener('change', onChange);
    }, []);

    useEffect(() => () => {
        if (rafRef.current !== null) cancelAnimationFrame(rafRef.current);
        if (pulseTimer.current !== null) window.clearTimeout(pulseTimer.current);
    }, []);

    // The single animation loop. It prunes finished particles, schedules the
    // next frame only if any remain, and asks React to re-render no more often
    // than roughly 30fps regardless of how many particles are on screen.
    const step = useCallback(() => {
        const now = performance.now();
        particlesRef.current = particlesRef.current.filter(p => now - p.startTs < p.lifetime);
        rafRef.current = particlesRef.current.length > 0 ? requestAnimationFrame(step) : null;
        if (rafRef.current === null || now - lastRenderRef.current >= FLOW_RENDER_INTERVAL_MS) {
            lastRenderRef.current = now;
            setFrame(f => f + 1);
        }
    }, []);

    const ensureLoop = useCallback(() => {
        if (rafRef.current === null) {
            lastRenderRef.current = 0;
            rafRef.current = requestAnimationFrame(step);
        }
    }, [step]);

    const triggerPulse = useCallback((rule: string) => {
        setPulse({rule, nonce: Date.now()});
        if (pulseTimer.current !== null) window.clearTimeout(pulseTimer.current);
        pulseTimer.current = window.setTimeout(() => setPulse(null), 1500);
    }, []);

    useEffect(() => {
        // First pass records where the buffer currently ends; nothing replays.
        if (lastSeenIdRef.current === null) {
            lastSeenIdRef.current = entries.length > 0 ? entries[0].id : 0;
            return;
        }
        const previous = lastSeenIdRef.current;
        const fresh = entries.filter(e => e.id > previous);
        if (entries.length > 0) lastSeenIdRef.current = entries[0].id;
        if (fresh.length === 0) return;

        const chaos = fresh
            .filter(e => isChaosTraffic(e.sessionName) && !!e.event)
            .sort((a, b) => a.id - b.id); // oldest first

        // Pulse the proxy for the newest event in the batch that fired a rule,
        // whether or not particles are animated.
        const firing = chaos.filter(e => e.event!.effects && e.event!.effects!.length > 0);
        if (firing.length > 0) {
            const effects = firing[firing.length - 1].event!.effects!;
            triggerPulse(effects[effects.length - 1].rule);
        }

        // Reduced motion, collapsed panel, or a hidden tab: flash + label only.
        if (reduceMotionRef.current || !openRef.current || document.hidden) return;

        const room = FLOW_PARTICLE_CAP - particlesRef.current.length;
        if (room <= 0) return; // full: skip, never queue

        // Keep the newest events when a burst overflows the cap.
        const spawn = chaos.slice(Math.max(0, chaos.length - room));
        const now = performance.now();
        for (const entry of spawn) {
            const classification = classifyEvent(entry.event!);
            particleIdRef.current += 1;
            particlesRef.current.push({
                id: particleIdRef.current,
                startTs: now,
                lifetime: flowLifetimeMs(classification),
                ...classification,
            });
        }
        ensureLoop();
    }, [entries, triggerPulse, ensureLoop]);

    // Clear in-flight particles when the session stops so a stale burst never
    // lingers behind the idle state.
    useEffect(() => {
        if (!running) {
            particlesRef.current = [];
            if (rafRef.current !== null) {
                cancelAnimationFrame(rafRef.current);
                rafRef.current = null;
            }
            setFrame(f => f + 1);
        }
    }, [running]);

    const now = performance.now();
    const particles = reduceMotion ? [] : particlesRef.current;

    return (
        <div className="chaos-flow">
            <button
                type="button"
                className="chaos-flow-header"
                onClick={() => setOpen(v => !v)}
                aria-expanded={open}
            >
                <span className={`log-chevron${open ? ' log-chevron--open' : ''}`} aria-hidden="true">
                    <ChevronDown size={14} strokeWidth={2} />
                </span>
                <span className="chaos-flow-title">Chaos Flow</span>
            </button>
            {open && (
                <div className="chaos-flow-body">
                    {running ? (
                        <div className="chaos-flow-canvas">
                            <div className="flow-track">
                            <svg
                                className="chaos-flow-lines"
                                viewBox="0 0 100 100"
                                preserveAspectRatio="none"
                                role="img"
                                aria-label="Diagram of chaos traffic moving from Your App, through the cha0s;sim proxy on port 8081, to the Backend."
                            >
                                <line x1={FLOW_X_APP} y1="50" x2={FLOW_X_PROXY} y2="50" vectorEffect="non-scaling-stroke" />
                                <line x1={FLOW_X_PROXY} y1="50" x2={FLOW_X_BACKEND} y2="50" vectorEffect="non-scaling-stroke" />
                            </svg>
                            {particles.map(p => {
                                const elapsed = now - p.startTs;
                                const afterProxy = elapsed > FLOW_SEGMENT_MS + p.pauseMs;
                                let color = 'var(--text-dim)';
                                if (p.outcome === 'override' && afterProxy) color = 'var(--severity-critical)';
                                else if (p.outcome === 'mangle' && afterProxy) color = 'var(--log-chaos)';
                                else if (p.outcome === 'fuzz' && afterProxy) color = 'var(--log-security)';
                                // Only the dot fades for a drop; the × lingers.
                                let dotOpacity = 1;
                                if (p.outcome === 'drop' && elapsed > FLOW_SEGMENT_MS) {
                                    dotOpacity = Math.max(0, 1 - (elapsed - FLOW_SEGMENT_MS) / FLOW_DROP_FADE_MS);
                                }
                                const showLatency = p.outcome === 'latency' && !!p.detail
                                    && elapsed >= FLOW_SEGMENT_MS
                                    && elapsed <= FLOW_SEGMENT_MS + p.pauseMs;
                                // Fuzz has no hold of its own, so its detail label
                                // rides along from the proxy to the backend.
                                const showFuzz = p.outcome === 'fuzz' && !!p.detail && elapsed >= FLOW_SEGMENT_MS;
                                return (
                                    <div key={p.id} className="flow-particle" style={{left: `${flowX(p, elapsed)}%`}}>
                                        <span className="flow-particle-dot" style={{background: color, opacity: dotOpacity}} />
                                        {showLatency && <span className="flow-particle-detail">{p.detail}</span>}
                                        {showFuzz && <span className="flow-particle-detail">{p.detail}</span>}
                                        {p.outcome === 'override' && p.statusLabel && afterProxy && (
                                            <span className="flow-particle-status">{p.statusLabel}</span>
                                        )}
                                        {p.outcome === 'drop' && elapsed > FLOW_SEGMENT_MS && (
                                            <span className="flow-particle-x" aria-hidden="true">&times;</span>
                                        )}
                                    </div>
                                );
                            })}
                            </div>
                            <div className="flow-node flow-node--app">
                                <Activity size={16} strokeWidth={1.8} aria-hidden="true" />
                                <span>Your App</span>
                            </div>
                            <div className="flow-node flow-node--proxy">
                                {pulse && <span className="flow-node-pulse" key={pulse.nonce} aria-hidden="true" />}
                                <Crosshair size={16} strokeWidth={1.8} aria-hidden="true" />
                                <span>cha0s;sim :{CHAOS_SESSION_PORT}</span>
                                {pulse && <span className="flow-node-rule" key={`rule-${pulse.nonce}`}>{pulse.rule}</span>}
                            </div>
                            <div className="flow-node flow-node--backend">
                                <Boxes size={16} strokeWidth={1.8} aria-hidden="true" />
                                <span>Backend</span>
                            </div>
                        </div>
                    ) : (
                        <div className="chaos-flow-idle">Start the Chaos session to see traffic flow</div>
                    )}
                    <div className="chaos-flow-caption">
                        Animation is reconstructed from completed requests; delays over 2s are capped for display.
                    </div>
                </div>
            )}
        </div>
    );
}
