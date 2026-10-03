import { useEffect, useRef, useState } from "preact/hooks";
import { apiWithDeadline, media } from "./api";
import { errStyle, ghostBtn, muted } from "./styles";
import { toast } from "./toast";

type ImportStatus = {
  status: "idle" | "running" | "done" | "error";
  added: number;
  failed: number;
  total: number;
};

type ImportResponse = ImportStatus | { status: number; error: string };

export function OPMLImport(props: { onDone: () => void }) {
  const [running, setRunning] = useState(false);
  const [message, setMessage] = useState("");
  const [error, setError] = useState("");
  const busy = useRef(false);
  const generation = useRef(0);
  const timer = useRef<ReturnType<typeof setTimeout>>();
  const done = useRef(props.onDone);
  done.current = props.onDone;

  const finish = (message: string, error = "") => {
    busy.current = false;
    setRunning(false);
    setMessage(message);
    setError(error);
  };

  const accept = (status: ImportStatus, id: number) => {
    if (id !== generation.current) return;
    setError("");
    if (status.status === "running") {
      busy.current = true;
      setRunning(true);
      setMessage(`Importing ${status.total} feeds: ${status.added} new, ${status.failed} failed so far`);
      timer.current = setTimeout(() => poll(id), 1000);
    } else if (status.status === "done") {
      const exists = Math.max(0, status.total - status.added - status.failed);
      const text = `Imported ${status.added} new, ${exists} already subscribed, ${status.failed} failed`;
      finish(text);
      toast(text, status.failed ? "default" : "success");
      done.current();
    } else {
      finish("", status.status === "idle" ? "Import status was lost. Check your subscriptions before importing again." : "The import stopped before completion.");
      done.current();
    }
  };

  const poll = async (id: number) => {
    try {
      const status: ImportResponse = await apiWithDeadline("/podcasts/import-opml/status");
      if (id !== generation.current) return;
      if ("error" in status) {
        if (status.status >= 500 || status.status === 408 || status.status === 429) throw new Error(status.error);
        finish("", status.error);
        return;
      }
      accept(status, id);
    } catch {
      if (id !== generation.current) return;
      setError("Couldn't refresh import status. Retrying…");
      timer.current = setTimeout(() => poll(id), 3000);
    }
  };

  useEffect(() => {
    const id = ++generation.current;
    apiWithDeadline("/podcasts/import-opml/status").then((status: ImportStatus) => {
      if (id === generation.current && status.status === "running") accept(status, id);
    }).catch(() => {});
    return () => { ++generation.current; clearTimeout(timer.current); };
  }, []);

  const start = async (file: File) => {
    if (busy.current) return;
    busy.current = true;
    const id = ++generation.current;
    clearTimeout(timer.current);
    setRunning(true);
    setMessage("Importing…");
    setError("");
    try {
      const opml = await file.text();
      if (id !== generation.current) return;
      const status: ImportResponse = await apiWithDeadline("/podcasts/import-opml", { method: "POST", body: JSON.stringify({ opml }) });
      if (id !== generation.current) return;
      if ("error" in status) {
        if (status.status === 409 && status.error === "import already running") { await poll(id); return; }
        finish("", status.error);
        return;
      }
      accept(status, id);
    } catch {
      if (id !== generation.current) return;
      finish("", "Couldn't confirm whether the import started. Reopen Podcasts to check its status.");
    }
  };

  return (
    <div style={{ marginTop: "1.2rem" }}>
      <div style={{ display: "flex", gap: "0.6rem", alignItems: "center", flexWrap: "wrap" }}>
        <label style={{ ...ghostBtn, cursor: running ? "default" : "pointer", opacity: running ? 0.6 : 1 }}>
          Import OPML
          <input type="file" accept=".opml,application/xml,text/xml,text/x-opml" disabled={running} style={{ display: "none" }} onChange={(event) => {
            const input = event.target as HTMLInputElement;
            const file = input.files?.[0];
            if (file) start(file);
            input.value = "";
          }} />
        </label>
        <a style={{ ...ghostBtn, textDecoration: "none" }} href={media("/podcasts/export-opml")} download="libteca-podcasts.opml">Export OPML</a>
        {message && <span style={muted} role="status">{message}</span>}
      </div>
      {error && <p style={errStyle} role="alert">{error}</p>}
    </div>
  );
}
