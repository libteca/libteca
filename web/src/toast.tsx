import { useEffect, useRef, useState } from "preact/hooks";

export type ToastKind = "default" | "success" | "error";

type ToastItem = { id: number; msg: string; kind: ToastKind; leaving: boolean };

let seq = 0;
let push: ((msg: string, kind: ToastKind) => void) | null = null;

export function toast(msg: string, kind: ToastKind = "default") {
  push?.(msg, kind);
}

const TOAST_CSS = `
@keyframes libteca-toast-in { from { opacity: 0; transform: translateX(1.3rem); } to { opacity: 1; transform: none; } }
@keyframes libteca-toast-out { from { opacity: 1; transform: none; } to { opacity: 0; transform: translateX(1.3rem); } }
.toast-host {
  position: fixed; right: 1.4rem; bottom: calc(1.4rem + env(safe-area-inset-bottom)); z-index: 70;
  display: flex; flex-direction: column; align-items: flex-end; gap: 0.55rem;
  pointer-events: none;
}
body:has(.player-bar) .toast-host { bottom: 6rem; }
.toast-card {
  pointer-events: auto; max-width: 22rem; padding: 0.68rem 0.95rem; border-radius: 12px;
  background: rgba(20,21,24,0.78); border: 1px solid #26282d; border-left: 2px solid #3a3d45;
  backdrop-filter: blur(18px) saturate(160%); -webkit-backdrop-filter: blur(18px) saturate(160%);
  box-shadow: 0 14px 34px rgba(0,0,0,0.45);
  color: #f5f5f7; font-size: 0.86rem; letter-spacing: -0.01em; line-height: 1.4;
  animation: libteca-toast-in 260ms var(--ease) both;
}
.toast-card.success { border-left-color: #32d74b; }
.toast-card.error { border-left-color: #ff6961; }
.toast-card.out { animation: libteca-toast-out 200ms ease both; }
@media (prefers-reduced-motion: reduce) { .toast-card, .toast-card.out { animation: none; } }
`;

export function ToastHost() {
  const [items, setItems] = useState<ToastItem[]>([]);
  const timers = useRef(new Map<number, number>());

  useEffect(() => {
    push = (msg, kind) => setItems((prev) => [...prev, { id: ++seq, msg, kind, leaving: false }].slice(-4));
    return () => { push = null; };
  }, []);

  useEffect(() => () => {
    for (const t of timers.current.values()) clearTimeout(t);
    timers.current.clear();
  }, []);

  const dismiss = (id: number) => {
    const t = timers.current.get(id);
    if (t) { clearTimeout(t); timers.current.delete(id); }
    setItems((prev) => prev.map((it) => (it.id === id ? { ...it, leaving: true } : it)));
    setTimeout(() => {
      timers.current.delete(id);
      setItems((prev) => prev.filter((it) => it.id !== id));
    }, 200);
  };

  useEffect(() => {
    for (const it of items) {
      if (it.leaving || timers.current.has(it.id)) continue;
      timers.current.set(it.id, window.setTimeout(() => dismiss(it.id), 2800));
    }
  }, [items]);

  return (
    <div className="toast-host" role="status" aria-live="polite">
      <style>{TOAST_CSS}</style>
      {items.map((it) => (
        <div key={it.id} className={`toast-card ${it.kind}${it.leaving ? " out" : ""}`}>{it.msg}</div>
      ))}
    </div>
  );
}
