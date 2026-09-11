import { useEffect, useRef, useState } from "preact/hooks";
import { api, getToken, setToken } from "./api";
import { globalCss, brand, c, center, content, headerBar, headerInner, input, linkBtn, muted, nav, navLink, page, primaryBtn } from "./styles";
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

const ROUTE_NAMES = ["home", "library", "work", "read", "search", "podcasts", "playlists", "admin", "matching"];

function routeNum(v: string | null): number | undefined {
  if (v == null) return undefined;
  const n = Number(v);
  return Number.isFinite(n) && n > 0 ? Math.floor(n) : undefined;
}

function parseHash(): View {
  const h = (typeof location === "undefined" ? "" : location.hash).replace(/^#\//, "");
  const [name, qs] = h.split("?");
  const params = new URLSearchParams(qs || "");
  const id = routeNum(params.get("id"));
  const q = params.get("q");
  const lib = routeNum(params.get("lib"));
  const edition = routeNum(params.get("edition"));
  const format = params.get("format");
  if (name === "read" && edition) return { name: "read", edition, id, format: format || undefined };
  if (name === "work" && id) return { name: "work", id };
  if (name === "admin") return { name: "admin" };
  if (name === "matching") return { name: "matching" };
  if (name === "search") return { name: "search", q: q || "" };
  if (name === "podcasts") return { name: "podcasts" };
  if (name === "playlists") return { name: "playlists", id };
  if (name === "home") return { name: "home" };
  const typ = params.get("type");
  if (name === "library") return { name: "library", lib, type: typ || undefined };
  return { name: "home" };
}

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
    api("/me").then((u) => {
      if (u && typeof u.id === "number") { setMe(u); setBootErr(false); setReady(true); }
      else { setBootErr(true); setReady(true); }
    }).catch(() => {
      if (getToken()) setBootErr(true);
      setReady(true);
    });
  };

  useEffect(() => {
    setToken(localStorage.getItem("libteca-token") || "");
    if (location.hash) {
      const h = location.hash.replace(/^#\/?/, "").split("?")[0];
      if (!ROUTE_NAMES.includes(h)) history.replaceState(null, "", "#/home");
    }
    if (!getToken()) { setReady(true); return; }
    loadMe();
    const onHash = () => { setView(parseHash()); setMenu(false); };
    addEventListener("hashchange", onHash);
    const onDoc = (e: MouseEvent) => {
      if (menuRef.current && !menuRef.current.contains(e.target as Node)) setMenu(false);
    };
    document.addEventListener("mousedown", onDoc);
    const onMenuKey = (e: KeyboardEvent) => { if (e.key === "Escape") setMenu(false); };
    addEventListener("keydown", onMenuKey);
    return () => {
      removeEventListener("hashchange", onHash);
      document.removeEventListener("mousedown", onDoc);
      removeEventListener("keydown", onMenuKey);
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
  if (!getToken()) return <div className="anim-page"><Login onLogin={() => { setView({ name: "home" }); location.hash = "#/home"; location.reload(); }} /></div>;
  if (!me) {
    if (bootErr) {
      return (
        <div style={{ ...center, flexDirection: "column", gap: "0.9rem" }}>
          <p style={{ ...muted, margin: 0 }}>Couldn't reach the server.</p>
          <button className="press btnp" style={primaryBtn} onClick={() => { setBootErr(false); loadMe(); }}>Retry</button>
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
      <div key={JSON.stringify(view)} className="anim-page pagecontent" style={content}>
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
  const formRef = useRef<HTMLFormElement | null>(null);

  const closeRef = useRef(props.onClose);
  closeRef.current = props.onClose;

  useEffect(() => {
    const prev = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    const focusables = (): HTMLElement[] => {
      const all = formRef.current?.querySelectorAll<HTMLElement>("input, button");
      return all ? [...Array.from(all)].filter((el) => !(el as HTMLButtonElement | HTMLInputElement).disabled) : [];
    };
    focusables()[0]?.focus();
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") { e.preventDefault(); closeRef.current(); return; }
      if (e.key !== "Tab") return;
      const els = focusables();
      if (els.length === 0) return;
      const first = els[0];
      const last = els[els.length - 1];
      if (e.shiftKey && document.activeElement === first) { e.preventDefault(); last.focus(); }
      else if (!e.shiftKey && document.activeElement === last) { e.preventDefault(); first.focus(); }
    };
    addEventListener("keydown", onKey);
    return () => {
      removeEventListener("keydown", onKey);
      prev?.focus();
    };
  }, []);

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
      className="anim-fade"
      onClick={(e) => { if (e.target === e.currentTarget) props.onClose(); }}
      style={{ position: "fixed", inset: 0, zIndex: 80, background: "rgba(8,9,11,0.72)", display: "flex", alignItems: "center", justifyContent: "center", padding: "1rem" }}
    >
      <form
        ref={formRef}
        className="anim-pop"
        onSubmit={submit}
        role="dialog"
        aria-modal="true"
        aria-label="Change password"
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
          <button className="press btnp" style={primaryBtn} type="submit" disabled={busy}>{busy ? "Updating…" : "Update"}</button>
        </div>
      </form>
    </div>
  );
}
