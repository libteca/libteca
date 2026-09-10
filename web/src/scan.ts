import { useEffect, useRef, useState } from "preact/hooks";
import { api, getToken, type ScanEvent } from "./api";

export type ScanState = {
  scanning: boolean;
  event: ScanEvent | null;
};

// POSTs a scan (tolerating "already running"), then follows it over SSE.
// The server's auth middleware accepts `?token=` for EventSource, which
// cannot send Authorization headers. On SSE failure falls back to polling
// GET /libraries/{id}/scan/jobs?limit=1 every 3s.
export function useScan(onDone?: (ev: ScanEvent) => void) {
  const [state, setState] = useState<ScanState>({ scanning: false, event: null });
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
    setState((s) => ({ ...s, scanning: true }));
    let finished = false;
    const terminal = (ev: ScanEvent) => {
      if (finished) return;
      finished = true;
      cleanup();
      setState({ scanning: false, event: ev });
      doneRef.current?.(ev);
    };

    const startPolling = () => {
      if (pollRef.current || finished) return;
      pollRef.current = window.setInterval(async () => {
        try {
          const jobs: ScanEvent[] = await api(`/libraries/${libId}/scan/jobs?limit=1`);
          const j = jobs?.[0];
          if (j && (j.status === "done" || j.status === "error")) terminal(j);
          else if (j) setState({ scanning: true, event: j });
        } catch { /* keep polling */ }
      }, 3000);
    };

    const es = new EventSource(`/api/core/libraries/${libId}/scan/events?token=${encodeURIComponent(getToken())}`);
    esRef.current = es;
    es.addEventListener("progress", (msg) => {
      let ev: ScanEvent;
      try { ev = JSON.parse((msg as MessageEvent).data); } catch { return; }
      setState({ scanning: ev.status === "running", event: ev });
      if (ev.status === "done" || ev.status === "error") terminal(ev);
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
      await api(`/libraries/${libId}/scan`, { method: "POST" });
    } catch { /* 409 already scanning is fine — we subscribe either way */ }
    follow(libId);
  };

  const attach = (libId: number) => follow(libId);

  return { ...state, start, attach };
}
