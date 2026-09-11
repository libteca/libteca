import { useRef } from "preact/hooks";
import type { ComponentChildren, CSSProperties } from "preact";
import { IconChevronLeft, IconChevronRight, IconPlay, IconSpinner } from "./svg";
import { iconBtn, railTitle } from "../styles";
import { Cover, type CoverRatio } from "./cover";
import { c, card, cardMeta, cardTitle, grid, gridSquare, muted, workCover, workHead, workMeta } from "../styles";

export function Rail(props: { title: string; children: ComponentChildren }) {
  const ref = useRef<HTMLDivElement | null>(null);
  const scrollBy = (dir: number) => {
    const el = ref.current;
    if (!el) return;
    const reduce = matchMedia("(prefers-reduced-motion: reduce)").matches;
    el.scrollBy({ left: dir * el.clientWidth * 0.85, behavior: reduce ? "auto" : "smooth" });
  };
  return (
    <section style={{ marginBottom: "2.4rem" }}>
      <div style={{ display: "flex", alignItems: "center", justifyContent: "space-between" }}>
        <h2 style={railTitle}>{props.title}</h2>
        <div className="rail-nav" style={{ display: "flex", gap: "0.15rem" }}>
          <button className="press" style={iconBtn} aria-label={`Scroll ${props.title} left`} onClick={() => scrollBy(-1)}>
            <IconChevronLeft size={16} />
          </button>
          <button className="press" style={iconBtn} aria-label={`Scroll ${props.title} right`} onClick={() => scrollBy(1)}>
            <IconChevronRight size={16} />
          </button>
        </div>
      </div>
      <div ref={ref} className="rail-x" style={{ display: "flex", gap: "1.05rem", overflowX: "auto", padding: "0.55rem 0.25rem 1rem", scrollSnapType: "x mandatory", scrollPaddingInline: "0.25rem" }}>
        {props.children}
      </div>
    </section>
  );
}

export function CardProgress(props: { pct: number }) {
  const pct = Math.max(0, Math.min(1, props.pct));
  return (
    <div style={{
      position: "absolute", left: "12%", right: "12%", bottom: "9%",
      height: "2.5px", borderRadius: "999px", background: "rgba(255,255,255,0.22)", overflow: "hidden",
    }}>
      <div style={{ display: "block", height: "100%", width: `${pct * 100}%`, background: "rgba(255,255,255,0.94)", borderRadius: "999px" }} />
    </div>
  );
}

export function RailCard(props: {
  id: number; title: string; meta?: string | null; hasCover: boolean;
  progress?: number; ratio?: CoverRatio; size?: number;
}) {
  const w = props.size ?? 11;
  return (
    <a href={`#/work?id=${props.id}`} className="rail-card" style={{ ...card, flex: `0 0 ${w}rem`, scrollSnapAlign: "start" }}>
      <div className="cardwrap">
        <Cover has={props.hasCover} id={props.id} title={props.title} ratio={props.ratio} />
        <div className="cardover"><div className="cardplay"><IconPlay size={18} /></div></div>
        {props.progress != null && props.progress > 0 && <CardProgress pct={props.progress} />}
      </div>
      <p style={cardTitle}>{props.title}</p>
      {props.meta && <p style={cardMeta}>{props.meta}</p>}
    </a>
  );
}

export function EmptyState(props: { title: string; hint?: string; icon?: ComponentChildren; children?: ComponentChildren }) {
  return (
    <div style={{
      padding: "1.4rem 0",
      display: "flex",
      flexDirection: "column",
      gap: "0.4rem",
      maxWidth: "34rem",
      alignItems: "flex-start",
    }}>
      {props.icon && (
        <span style={{ color: c.faint, opacity: 0.75, display: "inline-flex", marginBottom: "0.35rem" }}>{props.icon}</span>
      )}
      <p style={{ margin: 0, fontWeight: 600, fontSize: "1.02rem", letterSpacing: "-0.016em" }}>{props.title}</p>
      {props.hint && <p style={{ margin: 0, ...muted, fontSize: "0.88rem" }}>{props.hint}</p>}
      {props.children ? <div style={{ marginTop: "0.55rem" }}>{props.children}</div> : null}
    </div>
  );
}

export function QuietLoad() {
  return (
    <div style={{ display: "flex", justifyContent: "center", padding: "3.2rem 0" }} role="status" aria-label="Loading">
      <IconSpinner size={22} />
    </div>
  );
}

const skCard: CSSProperties = { display: "block" };
const skLine: CSSProperties = { height: "0.75rem", borderRadius: "999px", marginTop: "0.6rem" };

export function SkeletonGrid(props: { count?: number; square?: boolean }) {
  return (
    <div style={props.square ? gridSquare : grid}>
      {Array.from({ length: props.count ?? 12 }, (_, i) => (
        <div key={i} style={skCard}>
          <div className="sk" style={{ aspectRatio: props.square ? "1 / 1" : "2 / 3", borderRadius: "6px" }} />
          <div className="sk" style={{ ...skLine, width: "82%" }} />
          <div className="sk" style={{ ...skLine, width: "52%", height: "0.65rem" }} />
        </div>
      ))}
    </div>
  );
}

export function SkeletonRail(props: { count?: number; square?: boolean }) {
  return (
    <div style={{ display: "flex", gap: "1.05rem", overflow: "hidden", padding: "0.55rem 0.25rem 0", marginBottom: "2.4rem" }}>
      {Array.from({ length: props.count ?? 5 }, (_, i) => (
        <div key={i} style={{ ...skCard, flex: `0 0 12rem` }}>
          <div className="sk" style={{ aspectRatio: props.square ? "1 / 1" : "2 / 3", borderRadius: "6px" }} />
          <div className="sk" style={{ ...skLine, width: "80%" }} />
        </div>
      ))}
    </div>
  );
}

export function SkeletonWork() {
  return (
    <div style={workHead}>
      <div style={{ ...workCover, width: "13rem" }}>
        <div className="sk" style={{ aspectRatio: "2 / 3", borderRadius: "8px" }} />
      </div>
      <div style={{ ...workMeta, flex: 1, gap: "0.85rem", paddingTop: "0.4rem", width: "min(30rem, 100%)" }}>
        <div className="sk" style={{ height: "2.1rem", width: "68%", borderRadius: "8px" }} />
        <div className="sk" style={{ ...skLine, width: "38%", marginTop: 0 }} />
        <div className="sk" style={{ ...skLine, width: "54%", marginTop: 0 }} />
      </div>
    </div>
  );
}
