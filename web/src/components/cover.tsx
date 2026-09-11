import type { CSSProperties } from "preact";
import { useState } from "preact/hooks";
import { media } from "../api";
import { serif } from "../styles";

export type CoverRatio = "poster" | "square";

const CLOTH: { top: string; bot: string }[] = [
  { top: "#8b4a3a", bot: "#2c1612" },
  { top: "#3d6b58", bot: "#15241e" },
  { top: "#3a5278", bot: "#141c2c" },
  { top: "#8a6a32", bot: "#2a2010" },
  { top: "#5c3d6b", bot: "#1c1424" },
  { top: "#2f6b68", bot: "#122422" },
  { top: "#7a3f48", bot: "#241416" },
  { top: "#4a5a48", bot: "#161c16" },
];

function hash(s: string) {
  let h = 2166136261;
  for (let i = 0; i < s.length; i++) h = Math.imul(h ^ s.charCodeAt(i), 16777619);
  return Math.abs(h);
}

function monogram(title: string) {
  const words = title.trim().split(/\s+/).filter((w) => /[A-Za-z0-9]/.test(w));
  if (words.length >= 2) return (words[0][0] + words[1][0]).toUpperCase();
  const t = title.trim();
  return (t[0] || "?").toUpperCase();
}

const wrap: CSSProperties = { position: "relative", width: "100%" };

const img: CSSProperties = { width: "100%", height: "100%", objectFit: "cover", display: "block" };

function boxStyle(ratio?: CoverRatio): CSSProperties {
  return {
    width: "100%",
    aspectRatio: ratio === "square" ? "1 / 1" : "2 / 3",
    borderRadius: "6px",
    overflow: "hidden",
    display: "block",
    position: "relative",
    boxShadow: "0 16px 40px rgba(0,0,0,0.5)",
  };
}

function Cloth(props: { id: number; title: string; ratio?: CoverRatio }) {
  const pal = CLOTH[hash(props.title + ":" + props.id) % CLOTH.length];
  const mark = monogram(props.title);
  const square = props.ratio === "square";
  return (
    <div style={{
      position: "absolute", inset: 0,
      background: `linear-gradient(165deg, ${pal.top} 0%, ${pal.bot} 100%)`,
      display: "flex",
      flexDirection: "column",
      alignItems: "center",
      justifyContent: "center",
    }}>
      <span style={{
        fontFamily: serif,
        fontSize: square ? (mark.length > 1 ? "1.7rem" : "2.4rem") : (mark.length > 1 ? "2.2rem" : "3.1rem"),
        fontWeight: 600,
        color: "rgba(255,248,240,0.92)",
        letterSpacing: mark.length > 1 ? "0.06em" : "-0.03em",
        lineHeight: 1,
      }}>{mark}</span>
      <span style={{
        position: "absolute",
        left: "18%", right: "18%", bottom: "14%",
        height: "1px",
        background: "rgba(255,248,240,0.28)",
      }} />
    </div>
  );
}

export function Cover(props: { has: boolean; id: number; title: string; progress?: number; ratio?: CoverRatio }) {
  const [brokenId, setBrokenId] = useState<number | null>(null);
  const broken = brokenId === props.id;
  const pct = props.progress != null ? Math.max(0, Math.min(1, props.progress)) : null;
  const showImg = props.has && !broken;
  return (
    <div style={wrap}>
      <div className="cover-box" style={boxStyle(props.ratio)}>
        {showImg
          ? <img className="coverimg" style={img} src={media(`/covers/${props.id}.jpg`)} alt="" loading="lazy" onError={() => setBrokenId(props.id)} />
          : <Cloth id={props.id} title={props.title} ratio={props.ratio} />}
        {pct != null && pct > 0 && (
          <div style={{
            position: "absolute", left: "12%", right: "12%", bottom: "9%",
            height: "2.5px", borderRadius: "999px", background: "rgba(255,255,255,0.22)", overflow: "hidden",
          }}>
            <div style={{ display: "block", height: "100%", width: `${pct * 100}%`, background: "rgba(255,255,255,0.94)", borderRadius: "999px" }} />
          </div>
        )}
      </div>
    </div>
  );
}
