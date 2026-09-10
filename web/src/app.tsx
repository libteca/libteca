import { useEffect, useState } from "preact/hooks";
import { api, getToken, setToken } from "./api";
import { c, header as headerStyle, brand, center, linkBtn, nav, navLink, page } from "./styles";
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
import { IconHome, IconLibrary, IconMusic, IconPodcast } from "./components/svg";

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
.rail-x { scrollbar-width: none; }
.rail-x::-webkit-scrollbar { display: none; }
a { color: inherit; }
::selection { background: rgba(10, 132, 255, 0.35); }
select option { background: #141518; color: #f5f5f7; }
input[type="range"].seek { height: 3px; cursor: pointer; }
video::cue { background: rgba(0,0,0,0.7); }
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

  if (!ready) return <div style={center}>loading…</div>;
  if (!getToken()) return <Login onLogin={() => { setView({ name: "home" }); location.hash = "#/home"; location.reload(); }} />;
  if (!me) return <div style={center}>checking…</div>;

  const navItem = (active: boolean): preact.JSX.CSSProperties => ({
    ...navLink, color: active ? c.text : c.muted,
    display: "inline-flex", alignItems: "center", gap: "0.35rem",
  });

  return (
    <div style={page}>
      <style>{globalCss}</style>
      <header style={headerStyle}>
        <a href="#/home" style={brand}>libteca</a>
        <SearchBox />
        <nav style={nav}>
          <a href="#/home" style={navItem(view.name === "home")}><IconHome size={14} /> Home</a>
          <a href="#/library" style={navItem(view.name === "library")}><IconLibrary size={14} /> Library</a>
          <a href="#/podcasts" style={navItem(view.name === "podcasts")}><IconPodcast size={14} /> Podcasts</a>
          <a href="#/playlists" style={navItem(view.name === "playlists")}><IconMusic size={14} /> Playlists</a>
          {me.isAdmin && <a href="#/admin" style={navItem(view.name === "admin")}>Admin</a>}
          {me.isAdmin && <a href="#/matching" style={navItem(view.name === "matching")}>Matching</a>}
          <button style={linkBtn} onClick={() => { localStorage.removeItem("libteca-token"); setToken(""); location.hash = "#/"; location.reload(); }}>
            {me.name} · sign out
          </button>
        </nav>
      </header>
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
  );
}
