import { useEffect, useState } from "preact/hooks";
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
import { IconHome, IconLibrary, IconMusic, IconPodcast, IconSpinner } from "./components/svg";

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
::selection { background: rgba(10, 132, 255, 0.35); }
select option { background: #141518; color: #f5f5f7; }
video::cue { background: rgba(0,0,0,0.7); }

button, a, input, select { outline: none; }
button:focus-visible, a:focus-visible, input:focus-visible, select:focus-visible {
  outline: 2px solid #0a84ff;
  outline-offset: 2px;
}

.rail-card, .cover-card, .lib-tile {
  transition: transform 150ms var(--ease), filter 150ms ease;
}
.rail-card:hover, .cover-card:hover {
  transform: translateY(-2px);
  filter: brightness(1.08);
}
.rail-card:active, .cover-card:active { transform: translateY(0); filter: brightness(1.03); }
.lib-tile:hover { transform: translateY(-1px); filter: brightness(1.06); }

.press { transition: opacity 140ms ease, transform 140ms var(--ease); }
.press:active { opacity: 0.72; }

.row-hit { transition: background 140ms ease; }
.row-hit:hover { background: rgba(255,255,255,0.03); }

.search-wrap {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  padding: 0.28rem 0.85rem;
  border-radius: 999px;
  background: #141518;
  border: 1px solid #26282d;
  min-height: 36px;
  transition: border-color 140ms ease, box-shadow 140ms ease;
}
.search-wrap:focus-within {
  border-color: #0a84ff;
  box-shadow: 0 0 0 3px rgba(10, 132, 255, 0.18);
}

select.pill {
  appearance: none;
  -webkit-appearance: none;
  background: #141518;
  color: #c7c9ce;
  border: 1px solid #26282d;
  border-radius: 980px;
  padding: 0.35rem 2rem 0.35rem 0.95rem;
  font-size: 0.82rem;
  font-family: inherit;
  min-height: 36px;
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
  height: 5px;
  border-radius: 999px;
  background: transparent;
}
input[type="range"].seek::-webkit-slider-thumb {
  -webkit-appearance: none;
  width: 14px;
  height: 14px;
  border-radius: 50%;
  background: #f5f5f7;
  margin-top: -4.5px;
  border: none;
  box-shadow: 0 1px 4px rgba(0,0,0,0.45);
  transition: transform 140ms var(--ease);
}
input[type="range"].seek:hover::-webkit-slider-thumb { transform: scale(1.12); }
input[type="range"].seek::-moz-range-track {
  height: 5px;
  border-radius: 999px;
  background: transparent;
  border: none;
}
input[type="range"].seek::-moz-range-thumb {
  width: 14px;
  height: 14px;
  border-radius: 50%;
  background: #f5f5f7;
  border: none;
}

@keyframes libteca-spin { to { transform: rotate(360deg); } }
.spin { animation: libteca-spin 0.75s linear infinite; }

@media (max-width: 760px) {
  .nav-label { display: none; }
}

@media (prefers-reduced-motion: reduce) {
  .rail-card, .cover-card, .lib-tile, .press, .spin, .row-hit { transition: none; animation: none; }
  .rail-card:hover, .cover-card:hover, .lib-tile:hover { transform: none; filter: none; }
}
`;

export function App() {
  const [ready, setReady] = useState(false);
  const [view, setView] = useState<View>(() => parseHash());
  const [me, setMe] = useState<{ name: string; isAdmin: boolean } | null>(null);

  useEffect(() => {
    setToken(localStorage.getItem("libteca-token") || "");
    if (!getToken()) { setReady(true); return; }
    api("/me").then((u) => { setMe(u); setReady(true); }).catch(() => { setReady(true); });
    const onHash = () => setView(parseHash());
    addEventListener("hashchange", onHash);
    return () => removeEventListener("hashchange", onHash);
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
            <a href="#/home" style={navItem(view.name === "home")} aria-current={view.name === "home" ? "page" : undefined}>
              <IconHome size={15} /> <span className="nav-label">Home</span>
            </a>
            <a href="#/library" style={navItem(view.name === "library")} aria-current={view.name === "library" ? "page" : undefined}>
              <IconLibrary size={15} /> <span className="nav-label">Library</span>
            </a>
            <a href="#/podcasts" style={navItem(view.name === "podcasts")} aria-current={view.name === "podcasts" ? "page" : undefined}>
              <IconPodcast size={15} /> <span className="nav-label">Podcasts</span>
            </a>
            <a href="#/playlists" style={navItem(view.name === "playlists")} aria-current={view.name === "playlists" ? "page" : undefined}>
              <IconMusic size={15} /> <span className="nav-label">Playlists</span>
            </a>
            {me.isAdmin && <a href="#/admin" style={navItem(view.name === "admin")} aria-current={view.name === "admin" ? "page" : undefined}>Admin</a>}
            {me.isAdmin && <a href="#/matching" style={navItem(view.name === "matching")} aria-current={view.name === "matching" ? "page" : undefined}>Matching</a>}
          </nav>
          <SearchBox />
          <button
            className="press"
            style={{ ...linkBtn, marginLeft: "auto", flexShrink: 0, whiteSpace: "nowrap" }}
            onClick={() => { localStorage.removeItem("libteca-token"); setToken(""); location.hash = "#/"; location.reload(); }}
            aria-label={`Sign out ${me.name}`}
          >
            {me.name}
          </button>
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
