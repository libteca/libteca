import type { CSSProperties } from "preact";

export const font = "system-ui, -apple-system, 'SF Pro Text', 'Segoe UI', sans-serif";
export const serif = "'Iowan Old Style', 'Palatino Linotype', Palatino, Georgia, serif";
export const mono = "ui-monospace, SFMono-Regular, Menlo, monospace";

export const HEADER_H = "3.4rem";

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
  coverShadow: "0 8px 28px rgba(0,0,0,0.45)",
};

export const page: CSSProperties = {
  fontFamily: font,
  color: c.text,
  background: c.bg,
  minHeight: "100vh",
  margin: 0,
  padding: 0,
  lineHeight: 1.45,
  WebkitFontSmoothing: "antialiased",
};

export const content: CSSProperties = {
  maxWidth: "88rem",
  margin: "0 auto",
  padding: "1.15rem 1.6rem 7.5rem",
};

export const center: CSSProperties = {
  fontFamily: font,
  minHeight: "100vh",
  display: "flex",
  alignItems: "center",
  justifyContent: "center",
  color: c.muted,
  background: c.bg,
};

export const headerBar: CSSProperties = {
  position: "sticky",
  top: 0,
  zIndex: 30,
  background: "rgba(12, 13, 15, 0.82)",
  backdropFilter: "saturate(180%) blur(20px)",
  WebkitBackdropFilter: "saturate(180%) blur(20px)",
  borderBottom: `1px solid ${c.lineSoft}`,
};

export const headerInner: CSSProperties = {
  maxWidth: "88rem",
  margin: "0 auto",
  padding: "0 1.6rem",
  height: HEADER_H,
  display: "flex",
  alignItems: "center",
  gap: "0.85rem",
  flexWrap: "nowrap",
};

export const brand: CSSProperties = {
  fontSize: "1.05rem",
  fontWeight: 650,
  textDecoration: "none",
  color: c.text,
  letterSpacing: "-0.014em",
  flexShrink: 0,
};

export const nav: CSSProperties = {
  display: "flex",
  gap: "0.1rem",
  alignItems: "center",
  flexShrink: 0,
};

export const navLink: CSSProperties = {
  color: c.muted,
  textDecoration: "none",
  fontSize: "0.82rem",
  display: "inline-flex",
  alignItems: "center",
  gap: "0.35rem",
  padding: "0.35rem 0.6rem",
  minHeight: "36px",
  borderRadius: "8px",
};

export const linkBtn: CSSProperties = {
  background: "none",
  border: "none",
  color: c.muted,
  fontSize: "0.82rem",
  cursor: "pointer",
  fontFamily: font,
  padding: "0.35rem 0.5rem",
  minHeight: "36px",
  borderRadius: "8px",
};

export const loginWrap: CSSProperties = {
  minHeight: "100vh",
  display: "flex",
  alignItems: "center",
  justifyContent: "center",
  fontFamily: font,
  background: c.bg,
  padding: "1.5rem",
};

export const loginCard: CSSProperties = {
  display: "flex",
  flexDirection: "column",
  gap: "0.85rem",
  background: "none",
  border: "none",
  padding: 0,
  width: "20rem",
  maxWidth: "100%",
};

export const loginTitle: CSSProperties = {
  margin: 0,
  fontSize: "2rem",
  fontWeight: 650,
  letterSpacing: "-0.03em",
  lineHeight: 1.1,
};

export const loginSub: CSSProperties = {
  margin: "0 0 0.55rem",
  color: c.muted,
  fontSize: "1.05rem",
  letterSpacing: "-0.01em",
};

export const formCard: CSSProperties = {
  display: "flex",
  flexDirection: "column",
  gap: "0.7rem",
  background: c.bgRaised,
  border: `1px solid ${c.line}`,
  borderRadius: "14px",
  padding: "1.4rem",
  maxWidth: "26rem",
};

export const input: CSSProperties = {
  padding: "0.55rem 0.8rem",
  borderRadius: "10px",
  border: `1px solid ${c.line}`,
  fontSize: "0.95rem",
  fontFamily: font,
  background: c.bg,
  color: c.text,
  minWidth: 0,
  minHeight: "38px",
};

export const primaryBtn: CSSProperties = {
  background: c.accent,
  color: "#fff",
  border: "none",
  borderRadius: "980px",
  padding: "0.55rem 1.25rem",
  fontSize: "0.9rem",
  fontWeight: 600,
  cursor: "pointer",
  width: "fit-content",
  alignSelf: "flex-start",
  fontFamily: font,
  minHeight: "36px",
  display: "inline-flex",
  alignItems: "center",
  justifyContent: "center",
  gap: "0.45rem",
};

export const ghostBtn: CSSProperties = {
  background: "none",
  border: `1px solid ${c.line}`,
  borderRadius: "980px",
  padding: "0.4rem 0.95rem",
  fontSize: "0.85rem",
  cursor: "pointer",
  color: c.textDim,
  fontFamily: font,
  minHeight: "36px",
  display: "inline-flex",
  alignItems: "center",
  justifyContent: "center",
  gap: "0.4rem",
};

export const iconBtn: CSSProperties = {
  background: "none",
  border: "none",
  color: c.textDim,
  cursor: "pointer",
  padding: 0,
  display: "inline-flex",
  alignItems: "center",
  justifyContent: "center",
  borderRadius: "50%",
  width: "36px",
  height: "36px",
  flexShrink: 0,
};

export const errStyle: CSSProperties = { color: c.danger, margin: 0, fontSize: "0.85rem" };

export const muted: CSSProperties = { color: c.muted, fontSize: "0.88rem", margin: 0 };

export const rowBetween: CSSProperties = {
  display: "flex",
  justifyContent: "space-between",
  alignItems: "center",
  gap: "1rem",
  marginBottom: "1rem",
  flexWrap: "wrap",
};

export const tabRow: CSSProperties = { display: "flex", gap: "0.4rem", flexWrap: "wrap" };

export const tab: CSSProperties = {
  background: "none",
  border: "none",
  borderRadius: 0,
  padding: "0.35rem 0",
  fontSize: "0.9rem",
  cursor: "pointer",
  color: c.muted,
  fontFamily: font,
  minHeight: "36px",
  display: "inline-flex",
  alignItems: "center",
  textDecoration: "none",
  letterSpacing: "-0.01em",
};

export const tabActive: CSSProperties = {
  ...tab,
  color: c.text,
  fontWeight: 600,
};

export const grid: CSSProperties = {
  display: "grid",
  gridTemplateColumns: "repeat(auto-fill, minmax(10rem, 1fr))",
  gap: "1rem 0.8rem",
};

export const gridSquare: CSSProperties = {
  display: "grid",
  gridTemplateColumns: "repeat(auto-fill, minmax(9.25rem, 1fr))",
  gap: "1rem 0.8rem",
};

export const card: CSSProperties = {
  textDecoration: "none",
  color: "inherit",
  background: "none",
  border: "none",
  padding: 0,
  textAlign: "left",
  cursor: "pointer",
  fontFamily: font,
  display: "block",
};

export const cardTitle: CSSProperties = {
  margin: "0.5rem 0 0",
  fontWeight: 600,
  fontSize: "0.86rem",
  letterSpacing: "-0.01em",
  overflow: "hidden",
  textOverflow: "ellipsis",
  whiteSpace: "nowrap",
};

export const cardMeta: CSSProperties = {
  margin: 0,
  color: c.muted,
  fontSize: "0.76rem",
  overflow: "hidden",
  textOverflow: "ellipsis",
  whiteSpace: "nowrap",
};

export const badge: CSSProperties = {
  fontFamily: mono,
  fontSize: "0.7rem",
  letterSpacing: "0.04em",
  border: `1px solid ${c.line}`,
  background: c.bgRaised,
  color: c.textDim,
  borderRadius: "7px",
  padding: "0.16rem 0.5rem",
  display: "inline-block",
  lineHeight: 1.5,
};

export const workHead: CSSProperties = {
  display: "flex",
  gap: "2rem",
  marginBottom: "1.6rem",
  alignItems: "flex-start",
};

export const workCover: CSSProperties = { width: "12rem", flexShrink: 0, maxWidth: "42%" };

export const workMeta: CSSProperties = {
  display: "flex",
  flexDirection: "column",
  gap: "0.65rem",
  alignItems: "flex-start",
  minWidth: 0,
  paddingTop: "0.15rem",
};

export const workTitle: CSSProperties = {
  margin: 0,
  fontSize: "clamp(1.8rem, 3.2vw, 2.4rem)",
  fontWeight: 650,
  letterSpacing: "-0.024em",
  lineHeight: 1.12,
};

export const backLink: CSSProperties = {
  display: "inline-flex",
  alignItems: "center",
  gap: "0.25rem",
  color: c.textDim,
  textDecoration: "none",
  marginBottom: "1.15rem",
  fontSize: "0.88rem",
  background: "none",
  border: "none",
  cursor: "pointer",
  fontFamily: font,
  padding: "0.2rem 0.15rem",
  minHeight: "36px",
  borderRadius: "8px",
};

export const sectionTitle: CSSProperties = {
  margin: "0 0 0.9rem",
  fontSize: "1.25rem",
  fontWeight: 650,
  letterSpacing: "-0.02em",
};

export const railTitle: CSSProperties = {
  margin: "0 0 0.85rem",
  fontSize: "1.02rem",
  fontWeight: 650,
  letterSpacing: "-0.016em",
};

export const eyebrow: CSSProperties = {
  margin: "0 0 0.7rem",
  fontSize: "0.7rem",
  fontWeight: 600,
  textTransform: "uppercase",
  letterSpacing: "0.08em",
  color: c.faint,
};

export const chapterList: CSSProperties = { borderTop: `1px solid ${c.lineSoft}` };

export const chapterRow: CSSProperties = {
  display: "flex",
  justifyContent: "space-between",
  width: "100%",
  padding: "0.75rem 0.35rem",
  borderBottom: `1px solid ${c.lineSoft}`,
  background: "none",
  borderLeft: "none",
  borderRight: "none",
  borderTop: "none",
  cursor: "pointer",
  fontFamily: font,
  fontSize: "0.92rem",
  color: c.textDim,
  gap: "1rem",
  minHeight: "44px",
  alignItems: "center",
};

export const videoEl: CSSProperties = {
  width: "100%",
  height: "100%",
  background: "#000",
  display: "block",
  objectFit: "contain",
};

export function progressMini(pct: number): CSSProperties {
  return {
    width: "3rem",
    height: "4px",
    borderRadius: "999px",
    background: `linear-gradient(90deg, ${c.accent} ${Math.min(100, pct * 100)}%, ${c.line} ${Math.min(100, pct * 100)}%)`,
    alignSelf: "center",
    flexShrink: 0,
  };
}

export const playerBar: CSSProperties = {
  position: "fixed",
  bottom: 0,
  left: 0,
  right: 0,
  zIndex: 40,
  background: "rgba(18, 19, 22, 0.92)",
  backdropFilter: "blur(20px) saturate(160%)",
  WebkitBackdropFilter: "blur(20px) saturate(160%)",
  borderTop: `1px solid ${c.lineSoft}`,
  padding: "0.7rem 1.5rem 0.8rem",
  display: "flex",
  gap: "0.85rem",
  alignItems: "center",
  minHeight: "4.4rem",
};

export const adminRow: CSSProperties = {
  display: "flex",
  gap: "1rem",
  alignItems: "center",
  padding: "0.75rem 0",
  borderBottom: `1px solid ${c.lineSoft}`,
  minHeight: "44px",
};

export const table: CSSProperties = {
  width: "100%",
  borderCollapse: "collapse",
  fontSize: "0.85rem",
};

export const th: CSSProperties = {
  textAlign: "left",
  padding: "0.5rem 0.6rem",
  color: c.muted,
  fontWeight: 500,
  fontSize: "0.72rem",
  textTransform: "uppercase",
  letterSpacing: "0.06em",
  borderBottom: `1px solid ${c.line}`,
  whiteSpace: "nowrap",
};

export const td: CSSProperties = {
  padding: "0.6rem 0.6rem",
  borderBottom: `1px solid ${c.lineSoft}`,
  whiteSpace: "nowrap",
  color: c.textDim,
};

export function statusBar(_pct: number): CSSProperties {
  return {
    height: "4px",
    borderRadius: "999px",
    background: c.line,
    overflow: "hidden",
    display: "block",
  };
}

export const statusFill: CSSProperties = {
  display: "block",
  height: "100%",
  background: c.accent,
  borderRadius: "999px",
};

export const libToolbar: CSSProperties = {
  margin: "0 0 1.4rem",
  padding: 0,
  display: "flex",
  flexDirection: "column",
  gap: "0.65rem",
};

export const libTile: CSSProperties = {
  display: "flex",
  alignItems: "center",
  gap: "0.75rem",
  padding: "0.85rem 0.95rem",
  background: c.bgRaised,
  border: `1px solid ${c.lineSoft}`,
  borderRadius: "14px",
  textDecoration: "none",
  color: "inherit",
  minHeight: "3.6rem",
};

export const libTileIcon: CSSProperties = {
  width: "36px",
  height: "36px",
  borderRadius: "9px",
  background: c.bgHover,
  color: c.textDim,
  display: "inline-flex",
  alignItems: "center",
  justifyContent: "center",
  flexShrink: 0,
};

export const filterTrack: CSSProperties = {
  display: "flex",
  gap: "1rem",
  border: "none",
  padding: 0,
  background: "none",
};

export const filterBtn: CSSProperties = {
  border: "none",
  borderRadius: 0,
  padding: "0.28rem 0",
  fontSize: "0.82rem",
  cursor: "pointer",
  background: "none",
  color: c.muted,
  fontFamily: font,
  minHeight: "32px",
};

export const filterBtnOn: CSSProperties = {
  ...filterBtn,
  color: c.text,
  fontWeight: 600,
};

export const selectWrap: CSSProperties = {
  position: "relative",
  display: "inline-flex",
  alignItems: "center",
};

export const selectChevron: CSSProperties = {
  position: "absolute",
  right: "0.7rem",
  pointerEvents: "none",
  color: c.faint,
  display: "inline-flex",
};

export function gridFor(type?: string): CSSProperties {
  return type === "music" || type === "podcasts" ? gridSquare : grid;
}
