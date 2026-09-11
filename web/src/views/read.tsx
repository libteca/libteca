import { useCallback, useEffect, useState } from "preact/hooks";
import { api, media, type WorkDetail } from "../api";
import { CbzReader } from "../reader/cbz";
import { EpubReader } from "../reader/epub";
import { PdfReader } from "../reader/pdf";
import type { ReadingProgress } from "../reader/shared";
import { IconSpinner } from "../components/svg";
import { c, font, linkBtn } from "../styles";

type EditionInfo = { id: number; format: string; title: string; isFinished?: boolean };

export function ReadView(props: { edition: number; work?: number; format?: string }) {
  const [info, setInfo] = useState<EditionInfo | null>(null);
  const [progress, setProgress] = useState<ReadingProgress | null>(null);
  const [error, setError] = useState("");
  const [retryable, setRetryable] = useState(false);
  const [retry, setRetry] = useState(0);
  const [loaded, setLoaded] = useState(false);

  useEffect(() => {
    let alive = true;
    setLoaded(false);
    setInfo(null);
    setProgress(null);
    setError("");
    setRetryable(false);
    (async () => {
      let editionInfo: EditionInfo | null = null;
      try {
        if (props.work) {
          const w: WorkDetail = await api(`/works/${props.work}`);
          const ed = w?.editions?.find((e) => e.id === props.edition);
          if (ed) {
            editionInfo = { id: ed.id, format: ed.format.toLowerCase(), title: ed.title || w.title, isFinished: ed.isFinished };
          }
        } else if (props.format) {
          editionInfo = { id: props.edition, format: props.format.toLowerCase(), title: "" };
        }
      } catch { /* fall through to error state */ }
      if (!alive) return;
      if (!editionInfo) {
        setError(props.format ? "Edition not found." : "Missing work reference. Open this edition from its work page.");
        setLoaded(true);
        return;
      }
      setInfo(editionInfo);
      let p: ReadingProgress | null = null;
      try {
        p = await api(`/progress/${props.edition}`);
      } catch {
        if (!alive) return;
        setError("Couldn't load reading position");
        setRetryable(true);
        setLoaded(true);
        return;
      }
      if (!alive) return;
      setProgress(p || null);
      setLoaded(true);
    })();
    return () => { alive = false; };
  }, [props.edition, props.work, props.format, retry]);

  const onBack = useCallback(() => {
    location.hash = props.work ? `#/work?id=${props.work}` : "#/home";
  }, [props.work]);

  if (!loaded) {
    return <div style={paneStyle} role="status" aria-label="Loading"><IconSpinner size={22} /></div>;
  }
  if (error || !info) {
    return (
      <div style={paneStyle}>
        <span style={{ color: c.textDim, fontSize: "0.9rem" }}>{error || "Could not open this edition."}</span>
        <div style={{ display: "flex", gap: "0.9rem" }}>
          {retryable && <button style={{ ...linkBtn, color: c.textDim, fontSize: "0.9rem" }} onClick={() => setRetry((n) => n + 1)}>retry</button>}
          <button style={{ ...linkBtn, color: c.textDim, fontSize: "0.9rem" }} onClick={onBack}>back</button>
        </div>
      </div>
    );
  }
  if (info.format === "epub") {
    return <EpubReader editionId={props.edition} title={info.title || "EPUB"} progress={progress} onBack={onBack} />;
  }
  if (info.format === "cbz") {
    return <CbzReader editionId={props.edition} title={info.title || "CBZ"} progress={progress} onBack={onBack} />;
  }
  if (info.format === "pdf") {
    return <PdfReader editionId={props.edition} title={info.title || "PDF"} isFinished={!!progress?.isFinished} onBack={onBack} />;
  }
  return (
    <div style={paneStyle}>
      <span style={{ color: c.textDim, fontSize: "0.9rem" }}>{info.format.toUpperCase()} reading is not supported in-app yet.</span>
      <a style={{ ...linkBtn, color: c.textDim, fontSize: "0.9rem" }} href={media(`/editions/${props.edition}/download`)} download>download file</a>
    </div>
  );
}

const paneStyle = {
  position: "fixed", inset: 0, zIndex: 60, background: c.bg, fontFamily: font,
  display: "flex", flexDirection: "column", gap: "0.8rem", alignItems: "center", justifyContent: "center",
} as const;
