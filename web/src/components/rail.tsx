import { useRef } from "preact/hooks";
import type { ComponentChildren } from "preact";
import { IconChevronLeft, IconChevronRight } from "./svg";
import { c, iconBtn, railTitle } from "../styles";
import { Cover } from "./cover";
import { card, cardMeta, cardTitle } from "../styles";

export function Rail(props: { title: string; children: ComponentChildren }) {
  const ref = useRef<HTMLDivElement | null>(null);
  const scrollBy = (dir: number) => {
    const el = ref.current;
    if (el) el.scrollBy({ left: dir * el.clientWidth * 0.85, behavior: "smooth" });
  };
  return (
    <section style={{ marginBottom: "2.2rem" }}>
      <div style={{ display: "flex", alignItems: "center", justifyContent: "space-between" }}>
        <h2 style={railTitle}>{props.title}</h2>
        <div style={{ display: "flex", gap: "0.2rem" }}>
          <button style={iconBtn} aria-label={`Scroll ${props.title} left`} onClick={() => scrollBy(-1)}>
            <IconChevronLeft size={16} />
          </button>
          <button style={iconBtn} aria-label={`Scroll ${props.title} right`} onClick={() => scrollBy(1)}>
            <IconChevronRight size={16} />
          </button>
        </div>
      </div>
      <div ref={ref} className="rail-x" style={{ display: "flex", gap: "1rem", overflowX: "auto", paddingBottom: "0.4rem", scrollSnapType: "x proximity" }}>
        {props.children}
      </div>
    </section>
  );
}

export function RailCard(props: { id: number; title: string; meta?: string | null; hasCover: boolean; progress?: number }) {
  return (
    <a href={`#/work?id=${props.id}`} className="rail-card" style={{ ...card, flex: "0 0 8.5rem", scrollSnapAlign: "start" }}>
      <Cover has={props.hasCover} id={props.id} title={props.title} progress={props.progress} />
      <p style={cardTitle}>{props.title}</p>
      {props.meta && <p style={cardMeta}>{props.meta}</p>}
    </a>
  );
}

export function EmptyState(props: { title: string; hint?: string; children?: ComponentChildren }) {
  return (
    <div style={{
      padding: "1.6rem 1.4rem", border: `1px dashed ${c.line}`, borderRadius: "12px",
      display: "flex", flexDirection: "column", gap: "0.4rem", maxWidth: "34rem",
    }}>
      <p style={{ margin: 0, fontWeight: 600, fontSize: "0.95rem" }}>{props.title}</p>
      {props.hint && <p style={{ margin: 0, color: c.muted, fontSize: "0.85rem" }}>{props.hint}</p>}
      {props.children}
    </div>
  );
}
