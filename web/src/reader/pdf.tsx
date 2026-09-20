import { useEffect, useRef, useState } from "preact/hooks";
import { apiChecked, media } from "../api";
import { c, ghostBtn } from "../styles";
import { IconCheckSmall, IconDownload, isTypingTarget, pagePercent, readerOverlay, TopBar, useProgressSaver, type ReadingProgress } from "./shared";

export function PdfReader(props: { editionId: number; title: string; progress: ReadingProgress | null; onBack: () => void }) {
  const [finished, setFinished] = useState(!!props.progress?.isFinished);
  const [draft, setDraft] = useState("1");
  const [pageCount, setPageCount] = useState<number | undefined>();
  const [openPage, setOpenPage] = useState<number | null>(null);
  const [ready, setReady] = useState(false);
  const pageRef = useRef(1);
  const countRef = useRef<number | undefined>();
  const saver = useProgressSaver(props.editionId, props.progress?.revision ?? 0, {
    page: props.progress?.page, percent: props.progress?.percent,
  });
  const download = media(`/editions/${props.editionId}/download`);

  const bodyFor = (n: number) => {
    const out: { page: number; percent?: number } = { page: n };
    const count = countRef.current;
    if (count && count > 0) out.percent = pagePercent(n, count);
    return out;
  };

  const commit = (raw: string) => {
    const t = raw.trim();
    const n = t === "" ? NaN : Math.floor(Number(t));
    if (!isFinite(n)) {
      setDraft(String(pageRef.current));
      return;
    }
    let next = Math.max(1, n);
    const upper = countRef.current;
    if (upper && upper > 0) next = Math.min(next, upper);
    setDraft(String(next));
    setOpenPage(next);
    const changed = next !== pageRef.current;
    pageRef.current = next;
    if (changed) saver.save(bodyFor(next));
  };

  useEffect(() => {
    let alive = true;
    apiChecked<ReadingProgress & { pageCount?: number }>(`/progress/${props.editionId}`).then((p) => {
      if (!alive) return;
      const n = p?.page && p.page >= 1 ? Math.floor(p.page) : 0;
      if (n >= 1) {
        setDraft(String(n));
        if (!p.isFinished) setOpenPage(n);
        pageRef.current = n;
      }
      const pc = p?.pageCount;
      if (typeof pc === "number" && pc > 0) {
        setPageCount(pc);
        countRef.current = pc;
      }
      setReady(true);
    }).catch(() => {
      if (alive) setReady(true);
    });
    return () => { alive = false; };
  }, [props.editionId]);

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (isTypingTarget(e)) return;
      if (e.key === "Escape") props.onBack();
    };
    addEventListener("keydown", onKey);
    return () => removeEventListener("keydown", onKey);
  }, [props.onBack]);

  const markFinished = () => {
    setFinished(true);
    saver.save({ finished: true });
  };

  const src = openPage != null && openPage >= 1
    ? `${download}#toolbar=1&page=${openPage}`
    : `${download}#toolbar=1`;

  return (
    <div className="rd-in" style={readerOverlay}>
      <TopBar title={props.title} meta={finished ? "Finished" : "PDF · native viewer"} saveState={saver.state} onBack={props.onBack}>
        <span style={{ display: "inline-flex", alignItems: "center", gap: "0.25rem", color: c.muted, fontSize: "0.78rem", fontVariantNumeric: "tabular-nums" }}>
          <input
            type="number"
            inputMode="numeric"
            min={1}
            max={pageCount}
            value={draft}
            aria-label="Page"
            style={{
              width: "3.6rem",
              padding: "0.28rem 0.2rem",
              borderRadius: "999px",
              border: "1px solid rgba(255,255,255,0.08)",
              background: "rgba(255,255,255,0.06)",
              color: c.text,
              fontFamily: "inherit",
              fontSize: "0.78rem",
              fontVariantNumeric: "tabular-nums",
              textAlign: "center",
              appearance: "none",
              WebkitAppearance: "none",
              MozAppearance: "textfield",
            }}
            onInput={(e) => setDraft((e.target as HTMLInputElement).value)}
            onChange={(e) => commit((e.target as HTMLInputElement).value)}
            onBlur={(e) => commit((e.target as HTMLInputElement).value)}
            onKeyDown={(e) => {
              if (e.key === "Enter") {
                e.preventDefault();
                commit((e.target as HTMLInputElement).value);
                (e.target as HTMLInputElement).blur();
              }
            }}
          />
          {pageCount != null && pageCount > 0 ? <span>/ {pageCount}</span> : null}
        </span>
        {finished ? (
          <span style={{ display: "inline-flex", alignItems: "center", gap: "0.3rem", color: c.ok, fontSize: "0.78rem" }}>
            <IconCheckSmall size={14} /> Finished
          </span>
        ) : (
          <button style={ghostBtn} onClick={markFinished}>Mark finished</button>
        )}
        <a style={{ ...ghostBtn, display: "inline-flex", alignItems: "center", gap: "0.4rem", textDecoration: "none" }} href={download} download>
          <IconDownload size={14} /> Download
        </a>
      </TopBar>
      <div style={{ flex: 1, minHeight: 0, background: "#000" }}>
        {ready && <embed src={src} type="application/pdf" style={{ width: "100%", height: "100%", display: "block" }} />}
      </div>
    </div>
  );
}
