import type { CSSProperties } from "preact";

export const font = "system-ui, -apple-system, 'SF Pro Text', 'Segoe UI', sans-serif";
export const mono = "ui-monospace, SFMono-Regular, Menlo, monospace";

export const c = {
  bg: "#0c0d0f",
  bgRaised: "#141518",
  bgHover: "#1c1e22",
  line: "#26282d",
  lineSoft: "#1e2024",
  text: "#f5f5f7",
  textDim: "#c7c9ce",
  muted: "#86868b",
  faint: "#5c5f66",
  accent: "#0a84ff",
  accentSoft: "rgba(10, 132, 255, 0.14)",
  danger: "#ff6961",
  ok: "#32d74b",
  coverShadow: "0 2px 10px rgba(0,0,0,0.5)",
};

export const page: CSSProperties = {
  fontFamily: font,
  color: c.text,
  background: c.bg,
  minHeight: "100vh",
  margin: 0,
  padding: "0 1.4rem 7rem",
  lineHeight: 1.5,
  WebkitFontSmoothing: "antialiased",
};

export const center: CSSProperties = { fontFamily: font, padding: "4rem", textAlign: "center", color: c.muted };

export const header: CSSProperties = {
  display: "flex", justifyContent: "space-between", alignItems: "center", gap: "1rem",
  padding: "0.85rem 0", borderBottom: `1px solid ${c.lineSoft}`, marginBottom: "1.8rem",
  flexWrap: "wrap",
};

export const brand: CSSProperties = {
  fontSize: "1.1rem", fontWeight: 650, textDecoration: "none", color: c.text,
  letterSpacing: "-0.01em", flexShrink: 0,
};

export const nav: CSSProperties = { display: "flex", gap: "1rem", alignItems: "center", flexWrap: "wrap" };

export const navLink: CSSProperties = {
  color: c.textDim, textDecoration: "none", fontSize: "0.85rem",
};

export const linkBtn: CSSProperties = {
  background: "none", border: "none", color: c.muted, fontSize: "0.85rem",
  cursor: "pointer", fontFamily: font, padding: 0,
};

export const loginWrap: CSSProperties = {
  minHeight: "100vh", display: "flex", alignItems: "center", justifyContent: "center",
  fontFamily: font, background: c.bg,
};

export const loginCard: CSSProperties = {
  display: "flex", flexDirection: "column", gap: "0.7rem",
  background: c.bgRaised, border: `1px solid ${c.line}`, borderRadius: "14px",
  padding: "1.8rem", width: "20rem",
};

export const loginTitle: CSSProperties = { margin: "0 0 0.6rem", fontSize: "1.4rem", letterSpacing: "-0.02em" };

export const input: CSSProperties = {
  padding: "0.55rem 0.75rem", borderRadius: "9px", border: `1px solid ${c.line}`,
  fontSize: "0.95rem", fontFamily: font, background: c.bg, color: c.text,
  minWidth: 0,
};

export const primaryBtn: CSSProperties = {
  background: c.accent, color: "#fff", border: "none", borderRadius: "980px",
  padding: "0.55rem 1.2rem", fontSize: "0.9rem", fontWeight: 600, cursor: "pointer",
  width: "fit-content", alignSelf: "flex-start", fontFamily: font,
};

export const ghostBtn: CSSProperties = {
  background: "none", border: `1px solid ${c.line}`, borderRadius: "980px",
  padding: "0.4rem 0.95rem", fontSize: "0.85rem", cursor: "pointer", color: c.textDim,
  fontFamily: font,
};

export const iconBtn: CSSProperties = {
  background: "none", border: "none", color: c.textDim, cursor: "pointer",
  padding: "0.4rem", display: "inline-flex", alignItems: "center", justifyContent: "center",
  borderRadius: "50%",
};

export const errStyle: CSSProperties = { color: c.danger, margin: 0, fontSize: "0.85rem" };

export const muted: CSSProperties = { color: c.muted, fontSize: "0.88rem", margin: 0 };

export const rowBetween: CSSProperties = { display: "flex", justifyContent: "space-between", alignItems: "center", gap: "1rem", marginBottom: "1rem", flexWrap: "wrap" };

export const tabRow: CSSProperties = { display: "flex", gap: "0.4rem", flexWrap: "wrap" };

export const tab: CSSProperties = {
  background: "none", border: `1px solid ${c.line}`, borderRadius: "999px",
  padding: "0.3rem 0.9rem", fontSize: "0.85rem", cursor: "pointer", color: c.textDim,
  fontFamily: font,
};

export const tabActive: CSSProperties = { ...tab, background: c.text, color: c.bg, borderColor: c.text, fontWeight: 600 };

export const grid: CSSProperties = {
  display: "grid", gridTemplateColumns: "repeat(auto-fill, minmax(8.5rem, 1fr))", gap: "1.2rem",
};

export const card: CSSProperties = { textDecoration: "none", color: "inherit", background: "none", border: "none", padding: 0, textAlign: "left", cursor: "pointer", fontFamily: font, display: "block" };

export const cardTitle: CSSProperties = { margin: "0.5rem 0 0", fontWeight: 600, fontSize: "0.88rem", overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" };

export const cardMeta: CSSProperties = { margin: 0, color: c.muted, fontSize: "0.78rem", overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" };

export const badge: CSSProperties = {
  fontFamily: mono, fontSize: "0.72rem", letterSpacing: "0.04em",
  border: `1px solid ${c.line}`, background: c.bgRaised, color: c.textDim,
  borderRadius: "7px", padding: "0.18rem 0.55rem", display: "inline-block", lineHeight: 1.5,
};

export const workHead: CSSProperties = { display: "flex", gap: "1.6rem", marginBottom: "1.6rem", alignItems: "flex-start" };

export const workMeta: CSSProperties = { display: "flex", flexDirection: "column", gap: "0.7rem", alignItems: "flex-start", minWidth: 0 };

export const workTitle: CSSProperties = { margin: 0, fontSize: "1.6rem", letterSpacing: "-0.02em" };

export const backLink: CSSProperties = { display: "inline-block", color: c.textDim, textDecoration: "none", marginBottom: "1.2rem", fontSize: "0.9rem", background: "none", border: "none", cursor: "pointer", fontFamily: font, padding: 0 };

export const sectionTitle: CSSProperties = { margin: "0 0 0.9rem", fontSize: "1.2rem", letterSpacing: "-0.01em" };

export const railTitle: CSSProperties = { margin: "0 0 0.9rem", fontSize: "1rem", fontWeight: 650, letterSpacing: "-0.01em" };

export const chapterList: CSSProperties = { borderTop: `1px solid ${c.lineSoft}` };

export const chapterRow: CSSProperties = {
  display: "flex", justifyContent: "space-between", width: "100%",
  padding: "0.7rem 0.2rem", borderBottom: `1px solid ${c.lineSoft}`,
  background: "none", borderLeft: "none", borderRight: "none", borderTop: "none",
  cursor: "pointer", fontFamily: font, fontSize: "0.92rem", color: c.textDim, gap: "1rem",
};

export const videoEl: CSSProperties = {
  width: "100%", maxHeight: "72vh", background: "#000", borderRadius: "12px", display: "block",
};

export function progressMini(pct: number): CSSProperties {
  return { width: "3rem", height: "3px", borderRadius: "999px", background: `linear-gradient(90deg, ${c.accent} ${Math.min(100, pct * 100)}%, ${c.line} ${Math.min(100, pct * 100)}%)`, alignSelf: "center", flexShrink: 0 };
}

export const playerBar: CSSProperties = {
  position: "fixed", bottom: 0, left: 0, right: 0,
  background: "rgba(18, 19, 22, 0.92)", backdropFilter: "blur(16px)", WebkitBackdropFilter: "blur(16px)",
  borderTop: `1px solid ${c.lineSoft}`, padding: "0.7rem 1.4rem",
  display: "flex", gap: "0.9rem", alignItems: "center",
};

export const adminRow: CSSProperties = {
  display: "flex", gap: "1rem", alignItems: "center",
  padding: "0.7rem 0", borderBottom: `1px solid ${c.lineSoft}`,
};

export const table: CSSProperties = {
  width: "100%", borderCollapse: "collapse", fontSize: "0.85rem",
};

export const th: CSSProperties = {
  textAlign: "left", padding: "0.5rem 0.6rem", color: c.muted, fontWeight: 500,
  fontSize: "0.75rem", textTransform: "uppercase", letterSpacing: "0.06em",
  borderBottom: `1px solid ${c.line}`, whiteSpace: "nowrap",
};

export const td: CSSProperties = {
  padding: "0.55rem 0.6rem", borderBottom: `1px solid ${c.lineSoft}`, whiteSpace: "nowrap",
  color: c.textDim,
};

export function statusBar(pct: number): CSSProperties {
  return {
    height: "3px", borderRadius: "999px", background: c.line, overflow: "hidden", display: "block",
  };
}

export const statusFill: CSSProperties = {
  display: "block", height: "100%", background: c.accent, borderRadius: "999px",
};
