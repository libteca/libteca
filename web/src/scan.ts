import { useEffect, useRef, useState } from "preact/hooks";
import { apiWithDeadline, type ScanEvent } from "./api";

export type ScanState = {
  scanning: boolean;
  event: ScanEvent | null;
};

const emptyEvent = (libraryId: number, status: string, error?: string): ScanEvent => ({
  libraryId, status, error, filesSeen: 0, filesProbed: 0, filesAdded: 0, filesUpdated: 0, worksChanged: 0,
});

export function useScan(onDone?: (ev: ScanEvent) => void) {
  const [state, setState] = useState<ScanState>({ scanning: false, event: null });
  const esRef = useRef<EventSource | null>(null);
  const pollRef = useRef<ReturnType<typeof setTimeout>>();
  const generation = useRef(0);
  const doneRef = useRef(onDone);
  doneRef.current = onDone;

  const cleanup = () => {
    if (esRef.current) { esRef.current.close(); esRef.current = null; }
    clearTimeout(pollRef.current);
    pollRef.current = undefined;
  };

  useEffect(() => () => { ++generation.current; cleanup(); }, []);

  const begin = () => {
    const id = ++generation.current;
    cleanup();
    setState({ scanning: true, event: null });
    return id;
  };

  const follow = (libId: number, id: number, jobId?: number) => {
    let finished = false;
    let polling = false;
    const active = () => id === generation.current && !finished;
    const terminal = (event: ScanEvent) => {
      if (!active()) return;
      finished = true;
      cleanup();
      setState({ scanning: false, event });
      doneRef.current?.(event);
    };
    const update = (event: ScanEvent) => {
      if (!active()) return;
      if (event.status !== "running") terminal(event);
      else setState({ scanning: true, event });
    };

    const poll = async () => {
      pollRef.current = undefined;
      if (!active() || polling) return;
      polling = true;
      try {
        const result = await apiWithDeadline(jobId ? `/scan-jobs/${jobId}` : `/libraries/${libId}/scan/jobs?limit=1`);
        if (!active()) return;
        if (result?.error && typeof result.status === "number") {
          if (result.status >= 500 || result.status === 408 || result.status === 429) throw new Error(result.error);
          terminal(emptyEvent(libId, "error", result.error));
          return;
        }
        const event = jobId ? result : result?.[0];
        if (event) update(event);
        else if (Array.isArray(result)) terminal(emptyEvent(libId, "idle"));
      } catch {} finally { polling = false; }
      if (active()) pollRef.current = setTimeout(poll, 3000);
    };

    const startPolling = () => {
      if (!active() || polling || pollRef.current !== undefined) return;
      pollRef.current = setTimeout(poll, 3000);
    };

    try {
      const es = new EventSource(`/api/core/libraries/${libId}/scan/events`);
      esRef.current = es;
      es.addEventListener("progress", (msg) => {
        if (!active()) return;
        let event: ScanEvent;
        try { event = JSON.parse((msg as MessageEvent).data); } catch { return; }
        if (jobId && event.jobId !== jobId) {
          es.close();
          esRef.current = null;
          startPolling();
          return;
        }
        if (!jobId && event.jobId) jobId = event.jobId;
        update(event);
      });
      es.onerror = () => {
        if (!active()) return;
        es.close();
        esRef.current = null;
        startPolling();
      };
    } catch { startPolling(); }
  };

  const start = async (libId: number) => {
    const id = begin();
    try {
      const result = await apiWithDeadline(`/libraries/${libId}/scan`, { method: "POST" });
      if (id !== generation.current) return;
      const jobId = Number.isSafeInteger(result?.jobId) && result.jobId > 0 ? result.jobId : undefined;
      if (result?.error && !(result.status === 409 && jobId)) {
        setState({ scanning: false, event: emptyEvent(libId, "error", result.error) });
        return;
      }
      if (!jobId) {
        setState({ scanning: false, event: emptyEvent(libId, "error", "The server did not confirm a scan job.") });
        return;
      }
      follow(libId, id, jobId);
    } catch {
      if (id !== generation.current) return;
      setState({ scanning: false, event: emptyEvent(libId, "error", "Couldn't confirm whether the scan started. Check the scan log before retrying.") });
    }
  };

  const attach = (libId: number) => follow(libId, begin());

  return { ...state, start, attach };
}
