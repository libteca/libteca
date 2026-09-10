import { useEffect, useRef, useState } from "preact/hooks";
import { api, getToken } from "./api";

export type RefreshMetaEvent = {
  status: string;
  matched?: number;
  autoApplied?: number;
  total?: number;
  error?: string;
};

export type RefreshMetaState = {
  running: boolean;
  event: RefreshMetaEvent | null;
};

export function useRefreshMeta(onDone?: (ev: RefreshMetaEvent) => void) {
  const [state, setState] = useState<RefreshMetaState>({ running: false, event: null });
  const esRef = useRef<EventSource | null>(null);
  const pollRef = useRef<number | null>(null);
  const doneRef = useRef(onDone);
  doneRef.current = onDone;

  const cleanup = () => {
    if (esRef.current) { esRef.current.close(); esRef.current = null; }
    if (pollRef.current) { clearInterval(pollRef.current); pollRef.current = null; }
  };

  useEffect(() => cleanup, []);

  const follow = (libId: number) => {
    cleanup();
    setState((s) => ({ ...s, running: true }));
    let finished = false;
    const terminal = (ev: RefreshMetaEvent) => {
      if (finished) return;
      finished = true;
      cleanup();
      setState({ running: false, event: ev });
      doneRef.current?.(ev);
    };

    const startPolling = () => {
      if (pollRef.current || finished) return;
      pollRef.current = window.setInterval(async () => {
        try {
          const snap: RefreshMetaEvent = await api(`/libraries/${libId}/refresh-meta`);
          if (!snap) return;
          if (snap.status === "done" || snap.status === "error" || snap.status === "idle") terminal(snap);
          else setState({ running: true, event: snap });
        } catch { /* keep polling */ }
      }, 1000);
    };

    const es = new EventSource(`/api/core/libraries/${libId}/refresh-meta/events?token=${encodeURIComponent(getToken())}`);
    esRef.current = es;
    es.addEventListener("progress", (msg) => {
      let ev: RefreshMetaEvent;
      try { ev = JSON.parse((msg as MessageEvent).data); } catch { return; }
      setState({ running: ev.status === "running", event: ev });
      if (ev.status === "done" || ev.status === "error" || ev.status === "idle") terminal(ev);
    });
    es.onerror = () => {
      if (finished) return;
      es.close();
      esRef.current = null;
      startPolling();
    };
  };

  const start = async (libId: number) => {
    try {
      const res = await api(`/libraries/${libId}/refresh-meta`, { method: "POST" });
      if (res?.error && res.status !== "already_running") {
        setState({ running: false, event: { status: "error", error: res.error } });
        return;
      }
    } catch { /* 409 already running is fine — subscribe either way */ }
    follow(libId);
  };

  return { ...state, start };
}
