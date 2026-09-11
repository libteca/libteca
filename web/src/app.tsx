import { useEffect, useRef, useState } from "preact/hooks";
import { api, getToken, setToken } from "./api";
import { HEADER_H, brand, c, center, content, headerBar, headerInner, input, linkBtn, muted, nav, navLink, page, primaryBtn } from "./styles";
import { Login } from "./views/login";
import { Home } from "./views/home";
import { LibraryView } from "./views/library";
import { IconBook, IconComic, IconFilm, IconHeadphones, IconMusic, IconSpinner, IconTv } from "./components/svg";
import { ToastHost, toast } from "./toast";
import { isTypingTarget } from "./reader/shared";
import { focusSearch } from "./views/search";
import { typeLabel } from "./util";
import { WorkView } from "./views/work";
import { ReadView } from "./views/read";
import { SearchBox, SearchPage } from "./views/search";
import { AdminView } from "./views/admin";
import { MatchingView } from "./views/matching";
import { PodcastsView } from "./views/podcasts";
import { PlaylistsView } from "./views/playlists";

type View = { name: string; id?: number; q?: string; lib?: number; type?: string; edition?: number; format?: string };

function parseHash(): View {
  const h = (typeof location === "undefined" ? "" : location.hash).replace(/^#\//, "");
  const [name, qs] = h.split("?");
  const params = new URLSearchParams(qs || "");
  const id = params.get("id");
  const q = params.get("q");
  const lib = params.get("lib");
  const edition = params.get("edition");
  const format = params.get("format");
  if (name === "read" && edition) return { name: "read", edition: Number(edition), id: id ? Number(id) : undefined, format: format || undefined };
  if (name === "work" && id) return { name: "work", id: Number(id) };
  if (name === "admin") return { name: "admin" };
  if (name === "matching") return { name: "matching" };
  if (name === "search") return { name: "search", q: q || "" };
  if (name === "podcasts") return { name: "podcasts" };
  if (name === "playlists") return { name: "playlists", id: id ? Number(id) : undefined };
  if (name === "home") return { name: "home" };
  const typ = params.get("type");
  if (name === "library") return { name: "library", lib: lib ? Number(lib) : undefined, type: typ || undefined };
  return { name: "home" };
}

const globalCss = `
:root { --ease: cubic-bezier(0.22, 1, 0.36, 1); color-scheme: dark; }
html, body { margin: 0; background: #0c0d0f; }
.rail-x { scrollbar-width: none; }
.rail-x::-webkit-scrollbar { display: none; }
.topbar { height: ${HEADER_H}; padding: 0 1.6rem; }
a { color: inherit; }
::selection { background: rgba(10, 132, 255, 0.22); }
select option { background: #141518; color: #f5f5f7; }
video::cue { background: rgba(0,0,0,0.7); }

button, a, input, select { outline: none; }
button:focus-visible, a:focus-visible, input:focus-visible, select:focus-visible {
  outline: 2px solid #0a84ff;
  outline-offset: 2px;
}

.cover-box { transition: transform 0.45s var(--ease), box-shadow 0.45s var(--ease); transform-origin: 50% 80%; }
.rail-card:hover .cover-box, .cover-card:hover .cover-box {
  transform: translateY(-4px) scale(1.03);
  box-shadow: 0 22px 48px rgba(0,0,0,0.55);
}

.rail-nav { opacity: 0; transition: opacity 180ms ease; }
section:hover .rail-nav, section:focus-within .rail-nav { opacity: 1; }
@media (hover: none) { .rail-nav { opacity: 1; } }

.press { transition: opacity 140ms ease; }
.press:active { opacity: 0.7; }

.row-hit { transition: background 140ms ease; }
.row-hit:hover { background: rgba(255,255,255,0.028); }

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
.sk {
  background: linear-gradient(100deg, #141518 40%, #1e2025 50%, #141518 60%);
  background-size: 200% 100%;
  animation: libteca-shimmer 1.7s ease-in-out infinite;
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
}

@media (prefers-reduced-motion: reduce) {
  .cover-box, .press, .spin, .sk, .row-hit, .rail-nav { transition: none; animation: none; }
  .rail-card:hover .cover-box, .cover-card:hover .cover-box { transform: none; }
}
`;

function pageTitle(v: View): string {
  switch (v.name) {
    case "library": return v.type ? typeLabel(v.type) : "Library";
    case "work": return "Work";
    case "read": return "Reading";
    case "search": return v.q ? `Search: ${v.q}` : "Search";
    case "podcasts": return "Podcasts";
    case "playlists": return "Playlists";
    case "admin": return "Admin";
    case "matching": return "Matching";
    default: return "Home";
  }
}

export function App() {
  const [ready, setReady] = useState(false);
  const [view, setView] = useState<View>(() => parseHash());
  const [me, setMe] = useState<{ id: number; name: string; isAdmin: boolean } | null>(null);
  const [menu, setMenu] = useState(false);
  const [passOpen, setPassOpen] = useState(false);
  const [bootErr, setBootErr] = useState(false);
  const [navLibs, setNavLibs] = useState<{ id: number; type: string; name: string }[]>([]);
  const menuRef = useRef<HTMLDivElement | null>(null);

  const loadMe = () => {
    api("/libraries").then((l) => { if (Array.isArray(l)) setNavLibs(l); }).catch(() => {});
    api("/me").then((u) => { setMe(u); setBootErr(false); setReady(true); }).catch(() => {
      if (getToken()) setBootErr(true);
      setReady(true);
    });
  };

  useEffect(() => {
    setToken(localStorage.getItem("libteca-token") || "");
    if (!getToken()) { setReady(true); return; }
    loadMe();
    const onHash = () => { setView(parseHash()); setMenu(false); };
    addEventListener("hashchange", onHash);
    const onDoc = (e: MouseEvent) => {
      if (menuRef.current && !menuRef.current.contains(e.target as Node)) setMenu(false);
    };
    document.addEventListener("mousedown", onDoc);
    return () => {
      removeEventListener("hashchange", onHash);
      document.removeEventListener("mousedown", onDoc);
    };
  }, []);

  useEffect(() => {
    document.title = getToken() ? `libteca — ${pageTitle(view)}` : "libteca";
  }, [view]);

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key !== "/" || e.metaKey || e.ctrlKey || e.altKey || isTypingTarget(e)) return;
      e.preventDefault();
      focusSearch();
    };
    addEventListener("keydown", onKey);
    return () => removeEventListener("keydown", onKey);
  }, []);

  if (!ready) return <div style={center} role="status" aria-label="Loading"><IconSpinner size={22} /></div>;
  if (!getToken()) return <Login onLogin={() => { setView({ name: "home" }); location.hash = "#/home"; location.reload(); }} />;
  if (!me) {
    if (bootErr) {
      return (
        <div style={{ ...center, flexDirection: "column", gap: "0.9rem" }}>
          <p style={{ ...muted, margin: 0 }}>Couldn't reach the server.</p>
          <button className="press" style={primaryBtn} onClick={() => { setBootErr(false); loadMe(); }}>Retry</button>
        </div>
      );
    }
    return <div style={center} role="status" aria-label="Loading"><IconSpinner size={22} /></div>;
  }

  const navItem = (active: boolean): preact.JSX.CSSProperties => ({
    ...navLink, color: active ? c.text : c.muted, fontWeight: active ? 650 : 400,
  });

  return (
    <div style={page}>
      <style>{globalCss}</style>
      <header style={headerBar}>
        <div className="topbar" style={headerInner}>
          <a href="#/home" style={brand}>libteca</a>
          <nav style={nav}>
            <a href="#/home" style={navItem(view.name === "home")} aria-current={view.name === "home" ? "page" : undefined}>Home</a>
            {navLibs.length > 0 && [...new Set(navLibs.map((l) => l.type))].filter((t) => t !== "podcasts").map((t) => {
              const icon = { movies: <IconFilm size={13} />, tv: <IconTv size={13} />, music: <IconMusic size={13} />, audiobooks: <IconHeadphones size={13} />, books: <IconBook size={13} />, comics: <IconComic size={13} /> }[t];
              const on = view.name === "library" && view.type === t;
              return (
                <a key={t} href={`#/library?type=${t}`} style={navItem(on)} aria-current={on ? "page" : undefined}>
                  {icon}<span className="nav-cat">{typeLabel(t)}</span>
                </a>
              );
            })}
            <a href="#/library" style={navItem(view.name === "library" && !view.type)} aria-current={view.name === "library" && !view.type ? "page" : undefined}>Library</a>
            <a href="#/podcasts" style={navItem(view.name === "podcasts")} aria-current={view.name === "podcasts" ? "page" : undefined}>Podcasts</a>
          </nav>
          <SearchBox />
          <div ref={menuRef} style={{ marginLeft: "auto", flexShrink: 0, position: "relative" }}>
            <button
              className="press"
              style={{ ...linkBtn, whiteSpace: "nowrap" }}
              onClick={() => setMenu((v) => !v)}
              aria-expanded={menu}
              aria-haspopup="menu"
            >
              {me.name}
            </button>
            {menu && (
              <div className="menu" role="menu">
                <a href="#/playlists" role="menuitem">Playlists</a>
                {me.isAdmin && <a href="#/admin" role="menuitem">Admin</a>}
                {me.isAdmin && <a href="#/matching" role="menuitem">Matching</a>}
                <button
                  role="menuitem"
                  onClick={() => { setMenu(false); setPassOpen(true); }}
                >
                  Change password
                </button>
                <button
                  role="menuitem"
                  onClick={() => { localStorage.removeItem("libteca-token"); setToken(""); location.hash = "#/"; location.reload(); }}
                >
                  Sign out
                </button>
              </div>
            )}
          </div>
        </div>
      </header>
      <div style={content}>
        {view.name === "home" && <Home />}
        {view.name === "library" && <LibraryView lib={view.lib} type={view.type} />}
        {view.name === "work" && view.id != null && <WorkView id={view.id} />}
        {view.name === "read" && view.edition != null && <ReadView edition={view.edition} work={view.id} format={view.format} />}
        {view.name === "search" && <SearchPage q={view.q || ""} />}
        {view.name === "podcasts" && <PodcastsView />}
        {view.name === "playlists" && <PlaylistsView id={view.id} />}
        {view.name === "admin" && me.isAdmin && <AdminView />}
        {view.name === "matching" && me.isAdmin && <MatchingView />}
      </div>
      {passOpen && me && (
        <ChangePassModal
          userId={me.id}
          isAdmin={me.isAdmin}
          onClose={() => setPassOpen(false)}
        />
      )}
      <ToastHost />
    </div>
  );
}

function ChangePassModal(props: { userId: number; isAdmin: boolean; onClose: () => void }) {
  const [oldPass, setOldPass] = useState("");
  const [pass, setPass] = useState("");
  const [confirm, setConfirm] = useState("");
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");

  const submit = async (e: Event) => {
    e.preventDefault();
    setErr("");
    if (pass.length < 8) { setErr("New password must be at least 8 characters."); return; }
    if (pass !== confirm) { setErr("New passwords do not match."); return; }
    setBusy(true);
    try {
      const res: { error?: string } = await api(`/users/${props.userId}/password`, {
        method: "POST",
        body: JSON.stringify({ password: pass, oldPassword: oldPass }),
      });
      if (res.error) { setErr(res.error); return; }
      toast("Password updated — signing you in again", "success");
      setTimeout(() => { localStorage.removeItem("libteca-token"); setToken(""); location.hash = "#/"; location.reload(); }, 900);
    } catch {
      setErr("Failed to update password.");
    } finally {
      setBusy(false);
    }
  };

  return (
    <div
      onClick={(e) => { if (e.target === e.currentTarget) props.onClose(); }}
      style={{ position: "fixed", inset: 0, zIndex: 80, background: "rgba(8,9,11,0.72)", display: "flex", alignItems: "center", justifyContent: "center", padding: "1rem" }}
    >
      <form
        onSubmit={submit}
        style={{ width: "min(22rem, 100%)", background: c.bgRaised, border: `1px solid ${c.line}`, borderRadius: "14px", padding: "1.4rem", display: "flex", flexDirection: "column", gap: "0.8rem", boxShadow: "0 24px 60px rgba(0,0,0,0.5)" }}
      >
        <h2 style={{ margin: 0, fontSize: "1.05rem", color: c.text }}>Change password</h2>
        {!props.isAdmin && (
          <input style={input} type="password" placeholder="Current password" autoComplete="current-password" value={oldPass} onInput={(e) => setOldPass((e.target as HTMLInputElement).value)} />
        )}
        <input style={input} type="password" placeholder="New password (min 8 chars)" autoComplete="new-password" value={pass} onInput={(e) => setPass((e.target as HTMLInputElement).value)} />
        <input style={input} type="password" placeholder="Confirm new password" autoComplete="new-password" value={confirm} onInput={(e) => setConfirm((e.target as HTMLInputElement).value)} />
        {err && <p style={{ margin: 0, fontSize: "0.84rem", color: c.danger }}>{err}</p>}
        <div style={{ display: "flex", gap: "0.6rem", justifyContent: "flex-end" }}>
          <button className="press" type="button" onClick={props.onClose} style={{ ...linkBtn, color: c.muted }}>Cancel</button>
          <button className="press" style={primaryBtn} type="submit" disabled={busy}>{busy ? "Updating…" : "Update"}</button>
        </div>
      </form>
    </div>
  );
}
