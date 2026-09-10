import type { CSSProperties } from "preact";
import { media } from "../api";
import { c } from "../styles";

const HUES = ["#3a3f4a", "#443a4a", "#3a4a44", "#4a443a", "#46394a", "#39464a"];

function hueFor(id: number) {
  return HUES[Math.abs(id) % HUES.length];
}

const wrap: CSSProperties = { position: "relative", width: "100%" };

const box: CSSProperties = {
  width: "100%", aspectRatio: "2 / 3", borderRadius: "8px",
  overflow: "hidden", display: "block", boxShadow: c.coverShadow,
};

const img: CSSProperties = { width: "100%", height: "100%", objectFit: "cover", display: "block" };

const placeholder: CSSProperties = {
  ...box, background: c.bgRaised, display: "flex", alignItems: "center", justifyContent: "center",
};

const letter: CSSProperties = {
  color: c.textDim, fontSize: "2rem", fontWeight: 700,
  fontFamily: "'Iowan Old Style', Georgia, serif",
};

const barOuter: CSSProperties = {
  position: "absolute", left: "0.5rem", right: "0.5rem", bottom: "0.4rem",
  height: "3px", borderRadius: "999px", background: "rgba(255,255,255,0.25)", overflow: "hidden",
};

export function Cover(props: { has: boolean; id: number; title: string; progress?: number }) {
  const pct = props.progress != null ? Math.max(0, Math.min(1, props.progress)) : null;
  return (
    <div style={wrap}>
      {props.has
        ? <div style={box}><img style={img} src={media(`/covers/${props.id}.jpg`)} alt="" loading="lazy" /></div>
        : <div style={{ ...placeholder, background: hueFor(props.id) }}>
            <span style={letter}>{props.title.charAt(0).toUpperCase()}</span>
          </div>}
      {pct != null && pct > 0 && (
        <div style={barOuter}>
          <div style={{ display: "block", height: "100%", width: `${pct * 100}%`, background: c.accent, borderRadius: "999px" }} />
        </div>
      )}
    </div>
  );
}
