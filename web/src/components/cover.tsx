import type { CSSProperties } from "preact";
import { media } from "../api";
import { c, serif } from "../styles";

export type CoverRatio = "poster" | "square";

const HUES = ["#3a3f4a", "#443a4a", "#3a4a44", "#4a443a", "#46394a", "#39464a"];

function hueFor(id: number) {
  return HUES[Math.abs(id) % HUES.length];
}

const wrap: CSSProperties = { position: "relative", width: "100%" };

const img: CSSProperties = { width: "100%", height: "100%", objectFit: "cover", display: "block", willChange: "transform" };

const letter: CSSProperties = {
  color: "rgba(245, 245, 247, 0.82)",
  fontSize: "2.1rem",
  fontWeight: 700,
  fontFamily: serif,
};

const barOuter: CSSProperties = {
  position: "absolute", left: "0.5rem", right: "0.5rem", bottom: "0.45rem",
  height: "3px", borderRadius: "999px", background: "rgba(255,255,255,0.18)", overflow: "hidden",
};

export function Cover(props: { has: boolean; id: number; title: string; progress?: number; ratio?: CoverRatio }) {
  const pct = props.progress != null ? Math.max(0, Math.min(1, props.progress)) : null;
  const box: CSSProperties = {
    width: "100%",
    aspectRatio: props.ratio === "square" ? "1 / 1" : "2 / 3",
    borderRadius: "6px",
    overflow: "hidden",
    display: "block",
    boxShadow: "0 12px 32px rgba(0,0,0,0.38)",
  };
  return (
    <div style={wrap}>
      {props.has
        ? <div style={box}><img className="coverimg" style={img} src={media(`/covers/${props.id}.jpg`)} alt="" loading="lazy" /></div>
        : <div style={{ ...box, background: hueFor(props.id), display: "flex", alignItems: "center", justifyContent: "center" }}>
            <span style={letter}>{props.title.charAt(0).toUpperCase()}</span>
          </div>}
      {pct != null && pct > 0 && (
        <div style={barOuter}>
          <div style={{ display: "block", height: "100%", width: `${pct * 100}%`, background: "#fff", borderRadius: "999px" }} />
        </div>
      )}
    </div>
  );
}
