import { useEffect, useRef, useState } from "preact/hooks";
import { api, getToken, setToken } from "./api";
import { brand, c, center, content, headerBar, headerInner, linkBtn, nav, navLink, page } from "./styles";
import { Login } from "./views/login";
import { Home } from "./views/home";
import { LibraryView } from "./views/library";
import { WorkView } from "./views/work";
import { ReadView } from "./views/read";
import { SearchBox, SearchPage } from "./views/search";
import { AdminView } from "./views/admin";
import { MatchingView } from "./views/matching";
import { PodcastsView } from "./views/podcasts";
import { PlaylistsView } from "./views/playlists";
import { IconSpinner } from "./components/svg";

type View = { name: string; id?: number; q?: string; lib?: number; edition?: number; format?: string };

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
  if (name === "library") return { name: "library", lib: lib ? Number(lib) : undefined };
  return { name: "home" };
}

const globalCss = `
:root { --ease: cubic-bezier(0.22, 1, 0.36, 1); }
.rail-x, .topbar-x { scrollbar-width: none; }
.rail-x::-webkit-scrollbar, .topbar-x::-webkit-scrollbar { display: none; }
a { color: inherit; }
::selection { background: rgba(10, 132, 255, 0.22); }
select option { background: #141518; color: #f5f5f7; }
video::cue { background: rgba(0,0,0,0.7); }

button, a, input, select { outline: none; }
button:focus-visible, a:focus-visible, input:focus-visible, select:focus-visible {
  outline: 2px solid #0a84ff;
  outline-offset: 2px;
}

.coverimg { transition: transform 0.5s var(--ease); }
.rail-card:hover .coverimg, .cover-card:hover .coverimg { transform: scale(1.06); }

.rail-nav { opacity: 0; transition: opacity 180ms ease; }
section:hover .rail-nav, section:focus-within .rail-nav { opacity: 1; }

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

@media (max-width: 760px) {
  .nav-label { display: none; }
  .rail-nav { opacity: 1; }
}

@media (prefers-reduced-motion: reduce) {
  .coverimg, .press, .spin, .row-hit, .rail-nav { transition: none; animation: none; }
  .rail-card:hover .coverimg, .cover-card:hover .coverimg { transform: none; }
}
`;

export function App() {
  const [ready, setReady] = useState(false);
  const [view, setView] = useState<View>(() => parseHash());
  const [me, setMe] = useState<{ name: string; isAdmin: boolean } | null>(null);
  const [menu, setMenu] = useState(false);
  const menuRef = useRef<HTMLDivElement | null>(null);

  useEffect(() => {
    setToken(localStorage.getItem("libteca-token") || "");
    if (!getToken()) { setReady(true); return; }
    api("/me").then((u) => { setMe(u); setReady(true); }).catch(() => { setReady(true); });
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

  if (!ready) return <div style={center} role="status" aria-label="Loading"><IconSpinner size={22} /></div>;
  if (!getToken()) return <Login onLogin={() => { setView({ name: "home" }); location.hash = "#/home"; location.reload(); }} />;
  if (!me) return <div style={center} role="status" aria-label="Loading"><IconSpinner size={22} /></div>;

  const navItem = (active: boolean): preact.JSX.CSSProperties => ({
    ...navLink, color: active ? c.text : c.muted,
  });

  return (
    <div style={page}>
      <style>{globalCss}</style>
      <header style={headerBar}>
        <div className="topbar-x" style={{ ...headerInner, overflowX: "auto" }}>
          <a href="#/home" style={brand}>libteca</a>
          <nav style={nav}>
            <a href="#/home" style={navItem(view.name === "home")} aria-current={view.name === "home" ? "page" : undefined}>Home</a>
            <a href="#/library" style={navItem(view.name === "library")} aria-current={view.name === "library" ? "page" : undefined}>Library</a>
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
        {view.name === "library" && <LibraryView lib={view.lib} />}
        {view.name === "work" && view.id != null && <WorkView id={view.id} />}
        {view.name === "read" && view.edition != null && <ReadView edition={view.edition} work={view.id} format={view.format} />}
        {view.name === "search" && <SearchPage q={view.q || ""} />}
        {view.name === "podcasts" && <PodcastsView />}
        {view.name === "playlists" && <PlaylistsView id={view.id} />}
        {view.name === "admin" && me.isAdmin && <AdminView />}
        {view.name === "matching" && me.isAdmin && <MatchingView />}
      </div>
    </div>
  );
}
