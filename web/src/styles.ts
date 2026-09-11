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
  faint: "#767981",
  accent: "#0a84ff",
  accentSoft: "rgba(10, 132, 255, 0.14)",
  danger: "#ff6961",
  ok: "#32d74b",
  coverShadow: "0 8px 28px rgba(0,0,0,0.45)",
  accentGlow: "0 10px 30px rgba(10, 132, 255, 0.38)",
  ring: "0 0 0 1px rgba(255,255,255,0.09)",
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
  padding: "1.9rem 2rem 8rem",
  letterSpacing: "-0.011em",
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
  paddingTop: "env(safe-area-inset-top)",
  background: "rgba(12, 13, 15, 0.82)",
  backdropFilter: "saturate(180%) blur(20px)",
  WebkitBackdropFilter: "saturate(180%) blur(20px)",
  borderBottom: `1px solid ${c.lineSoft}`,
};

export const headerInner: CSSProperties = {
  maxWidth: "88rem",
  margin: "0 auto",
  display: "flex",
  alignItems: "center",
  gap: "0.85rem",
};

export const brand: CSSProperties = {
  fontFamily: serif,
  fontSize: "1.12rem",
  fontWeight: 600,
  textDecoration: "none",
  color: c.text,
  letterSpacing: "-0.022em",
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
  fontFamily: serif,
  fontSize: "2.15rem",
  fontWeight: 600,
  letterSpacing: "-0.03em",
  lineHeight: 1.1,
};

export const loginSub: CSSProperties = {
  margin: "0.4rem 0 0.9rem",
  color: c.faint,
  fontFamily: mono,
  fontSize: "0.68rem",
  textTransform: "uppercase",
  letterSpacing: "0.16em",
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
  boxShadow: `inset 0 -1.5px 0 ${c.text}`,
};

export const grid: CSSProperties = {
  display: "grid",
  gridTemplateColumns: "repeat(auto-fill, minmax(11.75rem, 1fr))",
  gap: "1.35rem 1.1rem",
};

export const gridSquare: CSSProperties = {
  display: "grid",
  gridTemplateColumns: "repeat(auto-fill, minmax(10.5rem, 1fr))",
  gap: "1.35rem 1.1rem",
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
  margin: "0.65rem 0 0",
  fontWeight: 600,
  fontSize: "0.92rem",
  letterSpacing: "-0.012em",
  overflow: "hidden",
  textOverflow: "ellipsis",
  whiteSpace: "nowrap",
};

export const cardTitleWrap: CSSProperties = {
  ...cardTitle,
  whiteSpace: "normal",
  display: "-webkit-box",
  WebkitLineClamp: 2,
  WebkitBoxOrient: "vertical",
  lineHeight: 1.3,
  minHeight: "2.2em",
};

export const cardMeta: CSSProperties = {
  margin: "0.15rem 0 0",
  color: c.muted,
  fontSize: "0.8rem",
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
  fontFamily: serif,
  fontSize: "clamp(1.85rem, 3.2vw, 2.55rem)",
  fontWeight: 600,
  letterSpacing: "-0.028em",
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
  margin: "0 0 1.05rem",
  fontSize: "1.42rem",
  fontWeight: 650,
  letterSpacing: "-0.02em",
};

export const railTitle: CSSProperties = {
  margin: "0 0 1rem",
  fontSize: "1.16rem",
  fontWeight: 700,
  letterSpacing: "-0.021em",
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
    height: "5px",
    borderRadius: "999px",
    background: `linear-gradient(90deg, ${c.accent} ${Math.min(100, pct * 100)}%, #3a3d45 ${Math.min(100, pct * 100)}%)`,
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
  padding: "0.7rem 1.5rem calc(0.8rem + env(safe-area-inset-bottom))",
  display: "flex",
  gap: "0.85rem",
  alignItems: "center",
  flexWrap: "wrap",
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
  boxShadow: `inset 0 -1.5px 0 ${c.text}`,
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

export const panel: CSSProperties = {
  background: c.bgRaised,
  border: `1px solid ${c.lineSoft}`,
  borderRadius: "16px",
  padding: "1.35rem 1.5rem",
  marginTop: "1.6rem",
};

export const panelHead: CSSProperties = {
  display: "flex",
  alignItems: "center",
  justifyContent: "space-between",
  gap: "1rem",
  flexWrap: "wrap",
  marginBottom: "0.9rem",
};

export const fieldLabel: CSSProperties = {
  margin: 0,
  fontSize: "0.7rem",
  fontWeight: 600,
  textTransform: "uppercase",
  letterSpacing: "0.07em",
  color: c.faint,
};

export const formBlock: CSSProperties = {
  display: "flex",
  flexDirection: "column",
  gap: "0.7rem",
  background: c.bg,
  border: `1px solid ${c.lineSoft}`,
  borderRadius: "12px",
  padding: "1.1rem 1.2rem",
  maxWidth: "28rem",
  marginTop: "1.2rem",
};

export const formNote: CSSProperties = { margin: 0, fontSize: "0.8rem", color: c.muted };

export const formNoteErr: CSSProperties = { ...formNote, color: c.danger };

export const preBlock: CSSProperties = {
  margin: 0,
  fontFamily: mono,
  fontSize: "0.8rem",
  lineHeight: 1.65,
  whiteSpace: "pre-wrap",
  wordBreak: "break-word",
  color: c.textDim,
  background: c.bg,
  border: `1px solid ${c.lineSoft}`,
  borderRadius: "12px",
  padding: "0.9rem 1rem",
};

export const sectionGap = "2.4rem";

export const globalCss = `
:root { --ease: cubic-bezier(0.22, 1, 0.36, 1); color-scheme: dark; -webkit-tap-highlight-color: transparent; }
html, body { margin: 0; background: #0c0d0f; }
* { scrollbar-width: thin; scrollbar-color: #2a2d33 transparent; }
*::-webkit-scrollbar { width: 8px; height: 8px; }
*::-webkit-scrollbar-thumb { background: #2a2d33; border-radius: 999px; }
*::-webkit-scrollbar-track { background: transparent; }
.rail-x { scrollbar-width: none; }
.rail-x::-webkit-scrollbar { display: none; }
.topbar { height: ${HEADER_H}; padding: 0 1.6rem; }
a { color: inherit; }
::selection { background: rgba(10, 132, 255, 0.22); }
::placeholder { color: #6f7278; opacity: 1; }
mark { background: rgba(10, 132, 255, 0.24); color: inherit; border-radius: 3px; padding: 0 0.06em; }
select option { background: #141518; color: #f5f5f7; }
video::cue { background: rgba(0,0,0,0.7); }

button, a, input, select { outline: none; }
button:focus-visible, a:focus-visible, input:focus-visible, select:focus-visible {
  outline: 2px solid #0a84ff;
  outline-offset: 2px;
}
button:disabled { opacity: 0.45; cursor: default; }

.cover-box { transition: transform 0.45s var(--ease), box-shadow 0.45s var(--ease); transform-origin: 50% 80%; }
.rail-card:hover .cover-box, .cover-card:hover .cover-box {
  transform: translateY(-4px) scale(1.03);
  box-shadow: 0 22px 48px rgba(0,0,0,0.55);
}

.rail-nav { opacity: 0; transition: opacity 180ms ease; }
section:hover .rail-nav, section:focus-within .rail-nav { opacity: 1; }
@media (hover: none) { .rail-nav { opacity: 1; } }
.rail-mask-l {
  -webkit-mask-image: linear-gradient(to right, transparent 0, #000 2rem);
  mask-image: linear-gradient(to right, transparent 0, #000 2rem);
}
.rail-mask-r {
  -webkit-mask-image: linear-gradient(to left, transparent 0, #000 2rem);
  mask-image: linear-gradient(to left, transparent 0, #000 2rem);
}
.rail-mask-l.rail-mask-r {
  -webkit-mask-image: linear-gradient(to right, transparent 0, #000 2rem, #000 calc(100% - 2rem), transparent);
  mask-image: linear-gradient(to right, transparent 0, #000 2rem, #000 calc(100% - 2rem), transparent);
}

.press { transition: opacity 140ms ease; }
.press:active { opacity: 0.7; }
.btnp { transition: box-shadow 220ms ease, filter 220ms ease, opacity 140ms ease; }
.btnp:hover:not(:disabled) { box-shadow: 0 10px 30px rgba(10, 132, 255, 0.38); filter: brightness(1.07); }

.row-hit { transition: background 140ms ease; }
.row-hit:hover { background: rgba(255,255,255,0.028); }
.menurow { transition: background 130ms ease, color 130ms ease; }
.menurow:hover { background: rgba(255,255,255,0.05); color: #f5f5f7; }

.libtile { transition: background 150ms ease, border-color 150ms ease; }
.libtile:hover { background: #1c1e22; border-color: #26282d; }

.search-wrap {
  display: flex;
  align-items: center;
  gap: 0.45rem;
  padding: 0.2rem 0.2rem 0.2rem 0.85rem;
  border-radius: 999px;
  background: transparent;
  border: 1px solid transparent;
  min-height: 34px;
  transition: background 160ms ease, border-color 160ms ease;
}
.search-wrap:hover, .search-wrap:focus-within {
  background: #141518;
  border-color: #26282d;
}

select.pill {
  appearance: none;
  -webkit-appearance: none;
  background: transparent;
  color: #86868b;
  border: none;
  padding: 0.25rem 1.6rem 0.25rem 0;
  font-size: 0.82rem;
  font-family: inherit;
  min-height: 32px;
  cursor: pointer;
}

input[type="range"].seek {
  -webkit-appearance: none;
  appearance: none;
  height: 36px;
  background: transparent;
  cursor: pointer;
  margin: 0;
  flex: 1;
  min-width: 4rem;
}
input[type="range"].seek::-webkit-slider-runnable-track {
  height: 3px;
  border-radius: 999px;
  background: transparent;
}
input[type="range"].seek::-webkit-slider-thumb {
  -webkit-appearance: none;
  width: 12px;
  height: 12px;
  border-radius: 50%;
  background: #f5f5f7;
  margin-top: -4.5px;
  border: none;
}
input[type="range"].seek::-moz-range-track {
  height: 3px; border-radius: 999px; background: transparent; border: none;
}
input[type="range"].seek::-moz-range-thumb {
  width: 12px; height: 12px; border-radius: 50%; background: #f5f5f7; border: none;
}

@keyframes libteca-shimmer { from { background-position: 200% 0; } to { background-position: -200% 0; } }
@keyframes libteca-fade { from { opacity: 0; } to { opacity: 1; } }
.sk {
  background: linear-gradient(100deg, #141518 40%, #1e2025 50%, #141518 60%);
  background-size: 200% 100%;
  animation: libteca-shimmer 1.7s ease-in-out infinite, libteca-fade 0.4s ease-out both;
  border-radius: 6px;
}

@keyframes libteca-spin { to { transform: rotate(360deg); } }
.spin { animation: libteca-spin 0.8s linear infinite; }

.menu {
  position: absolute; right: 0; top: calc(100% + 0.35rem);
  min-width: 10.5rem; padding: 0.35rem;
  background: #141518; border: 1px solid #26282d; border-radius: 12px;
  box-shadow: 0 18px 40px rgba(0,0,0,0.5); z-index: 60;
}
.menu a, .menu button {
  display: block; width: 100%; text-align: left;
  background: none; border: none; color: #c7c9ce;
  font: inherit; font-size: 0.84rem; padding: 0.5rem 0.7rem;
  border-radius: 8px; cursor: pointer; text-decoration: none;
}
.menu a:hover, .menu button:hover { background: rgba(255,255,255,0.05); color: #f5f5f7; }

.cardwrap { position: relative; }
.cardover {
  position: absolute; inset: 0; border-radius: 6px; overflow: hidden;
  opacity: 0; transition: opacity 200ms ease;
  background: linear-gradient(to top, rgba(0,0,0,0.82) 0%, rgba(0,0,0,0.25) 45%, transparent 70%);
  display: flex; align-items: flex-end; padding: 0.7rem;
}
.cardplay {
  position: absolute; top: 50%; left: 50%; transform: translate(-50%, -50%) scale(0.85);
  width: 46px; height: 46px; border-radius: 50%;
  background: rgba(10,132,255,0.92); color: #fff;
  display: flex; align-items: center; justify-content: center;
  box-shadow: 0 10px 30px rgba(10,132,255,0.45);
  transition: transform 200ms var(--ease);
}
.rail-card:hover .cardover, .cover-card:hover .cardover { opacity: 1; }
.rail-card:hover .cardplay, .cover-card:hover .cardplay { transform: translate(-50%, -50%) scale(1); }
.navlink {
  display: inline-flex; align-items: center; gap: 0.45rem;
  padding: 0.42rem 0.8rem; min-height: 36px; border-radius: 999px;
  color: #86868b; text-decoration: none; font-size: 0.86rem; font-weight: 500;
  transition: background 140ms ease, color 140ms ease;
}
.navlink:hover { color: #f5f5f7; background: rgba(255,255,255,0.05); }
.navlink.on { color: #f5f5f7; background: #1c1e22; font-weight: 650; }
nav a svg { flex-shrink: 0; }
@media (max-width: 1024px) { .nav-cat { display: none; } nav a { padding: 0.42rem 0.55rem; } }
@media (max-width: 760px) {
  .topbar { flex-wrap: wrap; height: auto; padding: 0.5rem 1rem; }
  .topbar nav { flex-grow: 1; }
  .topbar > div:first-of-type { min-width: 6rem !important; }
  .rail-nav { opacity: 1; }
}

@media (max-width: 640px) {
  input.seek { flex-basis: 100% !important; }
  .pagecontent { padding: 1.25rem 1.05rem 7rem !important; }
}

@media (max-width: 520px) {
  .cover-grid {
    grid-template-columns: repeat(auto-fill, minmax(8.75rem, 1fr)) !important;
    gap: 1.1rem 0.85rem !important;
  }
}

.login-field {
  background: transparent !important;
  border: none !important;
  border-bottom: 1px solid #26282d !important;
  border-radius: 0 !important;
  padding-left: 0 !important;
  transition: border-color 220ms ease, box-shadow 220ms ease;
}
.login-field:focus, .login-field:focus-visible {
  outline: none;
  border-color: #0a84ff !important;
  box-shadow: 0 1px 0 rgba(10, 132, 255, 0.35);
}
.login-field.login-err:not(:focus) { border-color: rgba(255, 105, 97, 0.55) !important; }
@keyframes libteca-rise { from { opacity: 0; transform: translateY(-0.2rem); } to { opacity: 1; transform: none; } }
.login-err-msg { animation: libteca-rise 0.25s ease-out both; }

@media (prefers-reduced-motion: reduce) {
  .cover-box, .press, .spin, .sk, .row-hit, .rail-nav, .btnp { transition: none; animation: none; }
  .rail-card:hover .cover-box, .cover-card:hover .cover-box { transform: none; }
  .login-err-msg { animation: none; }
}

@media (max-width: 768px) {
  .admin-panel { padding: 1rem !important; }
}
.danger-ghost:hover:not(:disabled) {
  border-color: rgba(255, 105, 97, 0.5) !important;
  background: rgba(255, 105, 97, 0.08) !important;
}
`;
