import { useRef } from "preact/hooks";
import type { ComponentChildren } from "preact";
import { IconChevronLeft, IconChevronRight, IconSpinner } from "./svg";
import { c, iconBtn, railTitle } from "../styles";
import { Cover, type CoverRatio } from "./cover";
import { card, cardMeta, cardTitle } from "../styles";

export function Rail(props: { title: string; children: ComponentChildren }) {
  const ref = useRef<HTMLDivElement | null>(null);
  const scrollBy = (dir: number) => {
    const el = ref.current;
    if (el) el.scrollBy({ left: dir * el.clientWidth * 0.85, behavior: "smooth" });
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
      <div ref={ref} className="rail-x" style={{ display: "flex", gap: "1.05rem", overflowX: "auto", padding: "0.15rem 0.15rem 0.7rem", scrollSnapType: "x mandatory", scrollPaddingInline: "0.15rem" }}>
        {props.children}
      </div>
    </section>
  );
}

export function RailCard(props: {
  id: number; title: string; meta?: string | null; hasCover: boolean;
  progress?: number; ratio?: CoverRatio; size?: number;
}) {
  const w = props.size ?? 11;
  return (
    <a href={`#/work?id=${props.id}`} className="rail-card" style={{ ...card, flex: `0 0 ${w}rem`, scrollSnapAlign: "start" }}>
      <Cover has={props.hasCover} id={props.id} title={props.title} progress={props.progress} ratio={props.ratio} />
      <p style={cardTitle}>{props.title}</p>
      {props.meta && <p style={cardMeta}>{props.meta}</p>}
    </a>
  );
}

export function EmptyState(props: { title: string; hint?: string; children?: ComponentChildren }) {
  return (
    <div style={{
      padding: "2rem 1.5rem",
      background: c.bgRaised,
      border: `1px solid ${c.lineSoft}`,
      borderRadius: "16px",
      display: "flex",
      flexDirection: "column",
      gap: "0.4rem",
      maxWidth: "34rem",
    }}>
      <p style={{ margin: 0, fontWeight: 600, fontSize: "1.02rem", letterSpacing: "-0.016em" }}>{props.title}</p>
      {props.hint && <p style={{ margin: 0, color: c.muted, fontSize: "0.88rem" }}>{props.hint}</p>}
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
