import { useEffect, useRef, useState } from "preact/hooks";

export type ToastKind = "default" | "success" | "error";

type ToastItem = { id: number; msg: string; kind: ToastKind; leaving: boolean };
type ToastRec = { deadline: number; timer: number };

let seq = 0;
let push: ((msg: string, kind: ToastKind) => void) | null = null;

export function toast(msg: string, kind: ToastKind = "default") {
  push?.(msg, kind);
}

const DURATION = 3500;
const EXIT_MS = 200;

const TOAST_CSS = `
.toast-host {
  position: fixed; right: 1.4rem; bottom: calc(1.4rem + env(safe-area-inset-bottom)); z-index: 70;
  display: flex; flex-direction: column; align-items: flex-end; gap: 0.55rem;
  pointer-events: none; max-width: calc(100vw - 2.8rem);
}
body:has(.player-bar) .toast-host { bottom: calc(6rem + env(safe-area-inset-bottom)); }
.toast-card {
  pointer-events: auto; width: fit-content; max-width: 22rem; box-sizing: border-box;
  display: flex; align-items: flex-start; gap: 0.55rem;
  padding: 0.68rem 0.95rem; border-radius: 12px; cursor: pointer;
  background: rgba(20,21,24,0.78); border: 1px solid #26282d; border-left: 2px solid #3a3d45;
  backdrop-filter: blur(18px) saturate(160%); -webkit-backdrop-filter: blur(18px) saturate(160%);
  box-shadow: 0 14px 34px rgba(0,0,0,0.45);
  color: #f5f5f7; font-size: 0.86rem; letter-spacing: -0.01em; line-height: 1.45;
  overflow-wrap: anywhere;
  animation: libteca-toast-in 240ms var(--ease) both;
}
.toast-card.success { border-left-color: #32d74b; }
.toast-card.error { border-left-color: #ff6961; }
.toast-card.out { animation: libteca-toast-out ${EXIT_MS}ms ease both; }
.toast-ico { flex-shrink: 0; display: inline-flex; margin-top: 0.1rem; color: #86868b; }
.toast-ico.success { color: #32d74b; }
.toast-ico.error { color: #ff6961; }
@media (max-width: 640px) { .toast-host { right: 0.75rem; } }
@media (prefers-reduced-motion: reduce) { .toast-card, .toast-card.out { animation: none; } }
`;

function IconCheck() {
  return (
    <svg width="14" height="14" viewBox="0 0 16 16" fill="none" aria-hidden="true">
      <path d="M3 8.6l3.3 3.3L13 4.8" stroke="currentColor" stroke-width="1.9" stroke-linecap="round" stroke-linejoin="round" />
    </svg>
  );
}

function IconAlert() {
  return (
    <svg width="14" height="14" viewBox="0 0 16 16" fill="none" aria-hidden="true">
      <path d="M8 2.1L15 13.6H1L8 2.1z" stroke="currentColor" stroke-width="1.6" stroke-linejoin="round" />
      <path d="M8 6.6v3" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" />
      <circle cx="8" cy="11.7" r="0.9" fill="currentColor" />
    </svg>
  );
}

export function ToastHost() {
  const [items, setItems] = useState<ToastItem[]>([]);
  const recs = useRef(new Map<number, ToastRec>());

  useEffect(() => {
    push = (msg, kind) => {
      const id = ++seq;
      const timer = window.setTimeout(() => dismiss(id), DURATION);
      recs.current.set(id, { deadline: Date.now() + DURATION, timer });
      setItems((prev) => [...prev, { id, msg, kind, leaving: false }].slice(-4));
    };
    return () => { push = null; };
  }, []);

  useEffect(() => () => {
    for (const r of recs.current.values()) clearTimeout(r.timer);
    recs.current.clear();
  }, []);

  const dismiss = (id: number) => {
    const rec = recs.current.get(id);
    if (rec) { clearTimeout(rec.timer); recs.current.delete(id); }
    setItems((prev) => prev.map((it) => (it.id === id ? { ...it, leaving: true } : it)));
    setTimeout(() => setItems((prev) => prev.filter((it) => it.id !== id)), EXIT_MS);
  };

  const pause = (id: number) => {
    const rec = recs.current.get(id);
    if (!rec) return;
    clearTimeout(rec.timer);
    rec.deadline = Math.max(rec.deadline - Date.now(), 0);
    rec.timer = 0;
  };

  const resume = (id: number) => {
    const rec = recs.current.get(id);
    if (!rec || rec.timer) return;
    const ms = Math.max(rec.deadline, EXIT_MS);
    rec.deadline = ms;
    rec.timer = window.setTimeout(() => dismiss(id), ms);
  };

  return (
    <div className="toast-host" role="status" aria-live="polite">
      <style>{TOAST_CSS}</style>
      {items.map((it) => (
        <div
          key={it.id}
          className={`toast-card ${it.kind}${it.leaving ? " out" : ""}`}
          onClick={() => dismiss(it.id)}
          onMouseEnter={() => pause(it.id)}
          onMouseLeave={() => resume(it.id)}
        >
          {it.kind === "success" && <span className="toast-ico success"><IconCheck /></span>}
          {it.kind === "error" && <span className="toast-ico error"><IconAlert /></span>}
          <span>{it.msg}</span>
        </div>
      ))}
    </div>
  );
}
