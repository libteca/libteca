import { useEffect, useState } from "preact/hooks";
import { api, media } from "../api";
import { c, ghostBtn } from "../styles";
import { IconCheckSmall, IconDownload, isTypingTarget, readerOverlay, TopBar } from "./shared";

export function PdfReader(props: { editionId: number; title: string; isFinished: boolean; onBack: () => void }) {
  const [finished, setFinished] = useState(props.isFinished);
  const [busy, setBusy] = useState(false);
  const src = `${media(`/editions/${props.editionId}/download`)}#toolbar=1`;

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (isTypingTarget(e)) return;
      if (e.key === "Escape") props.onBack();
    };
    addEventListener("keydown", onKey);
    return () => removeEventListener("keydown", onKey);
  }, [props.onBack]);

  const markFinished = async () => {
    setBusy(true);
    try {
      await api(`/progress/${props.editionId}`, { method: "POST", body: JSON.stringify({ finished: true }) });
      setFinished(true);
    } catch { /* surfaced by api redirect/state */ }
    setBusy(false);
  };

  return (
    <div style={readerOverlay}>
      <TopBar title={props.title} meta={finished ? "Finished" : "PDF · native viewer"} saveState={null} onBack={props.onBack}>
        {finished ? (
          <span style={{ display: "inline-flex", alignItems: "center", gap: "0.3rem", color: c.ok, fontSize: "0.78rem" }}>
            <IconCheckSmall size={14} /> Finished
          </span>
        ) : (
          <button style={{ ...ghostBtn, opacity: busy ? 0.6 : 1 }} disabled={busy} onClick={markFinished}>Mark finished</button>
        )}
        <a style={{ ...ghostBtn, display: "inline-flex", alignItems: "center", gap: "0.4rem", textDecoration: "none" }} href={media(`/editions/${props.editionId}/download`)} download>
          <IconDownload size={14} /> Download
        </a>
      </TopBar>
      <div style={{ flex: 1, minHeight: 0, background: "#000" }}>
        <embed src={src} type="application/pdf" style={{ width: "100%", height: "100%", display: "block" }} />
      </div>
    </div>
  );
}
