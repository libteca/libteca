import { useRef } from "preact/hooks";
import type { ComponentChildren } from "preact";
import { IconChevronLeft, IconChevronRight, IconPlay, IconSpinner } from "./svg";
import { iconBtn, railTitle } from "../styles";
import { Cover, type CoverRatio } from "./cover";
import { card, cardMeta, cardTitle, muted } from "../styles";

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

export function EmptyState(props: { title: string; hint?: string; children?: ComponentChildren }) {
  return (
    <div style={{
      padding: "1.4rem 0",
      display: "flex",
      flexDirection: "column",
      gap: "0.4rem",
      maxWidth: "34rem",
    }}>
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
