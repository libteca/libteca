import { useEffect, useRef, useState } from "preact/hooks";
import type { CSSProperties, ComponentChildren } from "preact";
import { api, type Library, type ScanEvent } from "../api";
import { useScan } from "../scan";
import { IconChevronDown, IconChevronLeft, IconScan } from "../components/svg";
import { fmtRel, typeLabel } from "../util";
import {
  backLink, badge, c, fieldLabel, formBlock, formNote, formNoteErr, ghostBtn, input, mono,
  muted, panel, panelHead, preBlock, primaryBtn, railTitle, sectionGap, sectionTitle,
  selectChevron, selectWrap, table, td, th,
} from "../styles";
import { toast } from "../toast";

const TYPES = ["audiobooks", "movies", "tv", "music", "books", "comics"];

const truncPath = (p: string) => (p.length > 42 ? `…${p.slice(-41)}` : p);

const adminStack: CSSProperties = { display: "flex", flexDirection: "column", gap: sectionGap };
const sectionPanel: CSSProperties = { ...panel, marginTop: 0, padding: "1.4rem 1.5rem" };
const headTitle: CSSProperties = { ...railTitle, margin: 0 };
const headDesc: CSSProperties = { ...panelHead, marginBottom: "0.35rem" };
const panelDesc: CSSProperties = { ...muted, margin: "0 0 0.9rem", lineHeight: 1.5 };
const adminForm: CSSProperties = { ...formBlock, gap: "0.9rem", maxWidth: "26rem", marginTop: "1.4rem" };
const checkLabel: CSSProperties = {
  display: "flex", gap: "0.45rem", alignItems: "center",
  fontSize: "0.88rem", color: c.textDim, cursor: "pointer",
};
const thA: CSSProperties = {
  ...th, fontFamily: mono, fontSize: "0.68rem", letterSpacing: "0.08em", padding: "0.55rem 0.75rem",
};
const tdA: CSSProperties = { ...td, padding: "0.55rem 0.75rem" };
const thNum: CSSProperties = { ...thA, textAlign: "right" };
const tdNum: CSSProperties = { ...tdA, textAlign: "right", fontVariantNumeric: "tabular-nums" };
const thAct: CSSProperties = { ...thA, textAlign: "right", padding: "0.55rem 0" };
const tdAct: CSSProperties = { ...tdA, textAlign: "right", padding: "0.55rem 0" };
const rowLine: CSSProperties = {
  display: "grid", gap: "0.9rem", alignItems: "center", minHeight: "44px",
  padding: "0.55rem 0", borderBottom: `1px solid ${c.lineSoft}`,
};
const libMain: CSSProperties = { display: "flex", gap: "0.7rem", alignItems: "center", flexWrap: "wrap", minWidth: 0 };
const libPath: CSSProperties = {
  fontFamily: mono, fontSize: "0.78rem", color: c.muted, flex: 1, minWidth: "6rem",
  overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap",
};
const rowActions: CSSProperties = { display: "inline-flex", gap: "0.5rem", alignItems: "center", flexShrink: 0 };
const provMain: CSSProperties = { display: "flex", gap: "0.7rem", alignItems: "center", flexWrap: "wrap", minWidth: 0 };
const provLabel: CSSProperties = { minWidth: "7.5rem", color: c.text, fontSize: "0.86rem" };
const provControls: CSSProperties = { display: "flex", gap: "0.5rem", alignItems: "center", minWidth: 0 };
const statusDot: CSSProperties = { width: "6px", height: "6px", borderRadius: "50%", display: "inline-block", flexShrink: 0 };

type JobRow = ScanEvent & { id: number; libraryId: number; startedAt: number; createdAt?: number; error?: string };

type AdminUser = { id: number; name: string; isAdmin: boolean; createdAtMs: number };

type TokenRow = {
  id: number; userId: number; label: string;
  createdAtMs: number; lastSeenAtMs: number | null; revokedAtMs: number | null;
};

type ProviderKey = { name: string; label: string; keyed: boolean; configured: boolean; masked: string; fromEnv: boolean };

function Field(props: { label: string; children: ComponentChildren }) {
  return (
    <label style={{ display: "flex", flexDirection: "column", gap: "0.35rem", minWidth: 0 }}>
      <span style={fieldLabel}>{props.label}</span>
      {props.children}
    </label>
  );
}

function StatusTag(props: { status: string; tone: string }) {
  return (
    <span style={{ display: "inline-flex", alignItems: "center", gap: "0.4rem", fontFamily: mono, fontSize: "0.76rem", color: props.tone }}>
      <span style={{ ...statusDot, background: props.tone }} />
      {props.status}
    </span>
  );
}

function useWide() {
  const [wide, setWide] = useState(() => window.matchMedia("(min-width: 769px)").matches);
  useEffect(() => {
    const mq = window.matchMedia("(min-width: 769px)");
    const on = () => setWide(mq.matches);
    mq.addEventListener("change", on);
    return () => mq.removeEventListener("change", on);
  }, []);
  return wide;
}

function FadeScroll(props: { children: ComponentChildren }) {
  const ref = useRef<HTMLDivElement>(null);
  const [fade, setFade] = useState({ l: false, r: false });
  const measure = () => {
    const el = ref.current;
    if (!el) return;
    const l = el.scrollLeft > 8;
    const r = el.scrollLeft + el.clientWidth < el.scrollWidth - 8;
    setFade((s) => (s.l === l && s.r === r ? s : { l, r }));
  };
  useEffect(() => {
    measure();
    const el = ref.current;
    if (!el) return;
    el.addEventListener("scroll", measure);
    window.addEventListener("resize", measure);
    return () => {
      el.removeEventListener("scroll", measure);
      window.removeEventListener("resize", measure);
    };
  });
  const mask = fade.l && fade.r
    ? "linear-gradient(to right, transparent 0, #000 1.5rem, #000 calc(100% - 1.5rem), transparent 100%)"
    : fade.l
      ? "linear-gradient(to right, transparent 0, #000 1.5rem)"
      : fade.r
        ? "linear-gradient(to left, transparent 0, #000 1.5rem)"
        : "none";
  return (
    <div ref={ref} style={{ overflowX: "auto", maskImage: mask, WebkitMaskImage: mask }}>
      {props.children}
    </div>
  );
}

export function AdminView() {
  const [libs, setLibs] = useState<Library[]>([]);
  const [name, setName] = useState("");
  const [type, setType] = useState("audiobooks");
  const [path, setPath] = useState("");
  const [msg, setMsg] = useState("");
  const [users, setUsers] = useState<AdminUser[]>([]);
  const [busy, setBusy] = useState(false);

  const refresh = () => api("/libraries").then((r) => { if (Array.isArray(r)) setLibs(r); }).catch(() => setLibs([]));
  const refreshUsers = () => api("/users").then((r: AdminUser[]) => setUsers(Array.isArray(r) ? r : [])).catch(() => setUsers([]));
  useEffect(() => { refresh(); refreshUsers(); }, []);

  const add = async (e: Event) => {
    e.preventDefault();
    if (busy) return;
    setBusy(true);
    setMsg("");
    try {
      const res = await api("/libraries", { method: "POST", body: JSON.stringify({ name, type, path }) });
      if (res.error) { setMsg(res.error); toast(res.error, "error"); return; }
      let scanRes: { error?: string };
      try {
        scanRes = await api(`/libraries/${res.id}/scan`, { method: "POST" });
      } catch {
        scanRes = { error: "network error" };
      }
      const addedName = name;
      setName(""); setPath("");
      refresh();
      if (scanRes.error) { setMsg(`Library added, but the scan failed to start: ${scanRes.error}`); toast(`Library "${addedName}" added, but the scan failed to start`, "error"); return; }
      setMsg("library added, scanning");
      toast(`Library "${addedName}" added — scan started`, "success");
    } catch {
      setMsg("Failed to add library.");
      toast("Failed to add library.", "error");
    } finally {
      setBusy(false);
    }
  };

  const addOk = msg === "library added, scanning";

  return (
    <div>
      <button className="press" style={backLink} onClick={() => history.back()}><IconChevronLeft size={16} /> Library</button>
      <h2 style={sectionTitle}>Admin</h2>

      <div style={adminStack}>
        <section className="admin-panel" style={sectionPanel}>
          <div style={panelHead}>
            <h3 style={headTitle}>Libraries</h3>
          </div>
          {libs.map((l) => <AdminLibRow key={l.id} lib={l} onRemoved={refresh} />)}
          {libs.length === 0 && <p style={muted}>No libraries configured.</p>}

          <form style={adminForm} onSubmit={add}>
            <Field label="Name">
              <input style={input} placeholder="Name" value={name} onInput={(e) => setName((e.target as HTMLInputElement).value)} />
            </Field>
            <Field label="Type">
              <div style={{ ...selectWrap, width: "100%" }}>
                <select className="pill" style={{ width: "100%" }} value={type} onChange={(e) => setType((e.target as HTMLSelectElement).value)} aria-label="Library type">
                  {TYPES.map((t) => <option key={t} value={t}>{typeLabel(t)}</option>)}
                </select>
                <span style={selectChevron}><IconChevronDown size={12} /></span>
              </div>
            </Field>
            <Field label="Path">
              <input style={input} placeholder="/path/to/media" value={path} onInput={(e) => setPath((e.target as HTMLInputElement).value)} />
            </Field>
            <button className="press btnp" style={primaryBtn} type="submit" disabled={busy}>{busy ? "Adding…" : "Add & scan"}</button>
            {msg && <p style={addOk ? formNote : formNoteErr}>{msg}</p>}
          </form>
        </section>

        <UsersSection users={users} onChanged={refreshUsers} />
        <TokensSection users={users} />
        <ImportSection />
        <ProvidersSection />
        <ScanJobs libs={libs} />
      </div>
    </div>
  );
}

function AdminLibRow(props: { lib: Library; onRemoved: () => void }) {
  const scan = useScan(() => {});
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);
  const wide = useWide();
  const line = scan.event && (scan.scanning
    ? `Scanning… ${scan.event.filesSeen} files${scan.event.currentPath ? ` · ${truncPath(scan.event.currentPath)}` : ""}`
    : scan.event.status === "error" ? `Failed${scan.event.error ? `: ${scan.event.error}` : ""}`
    : scan.event.filesAdded ? `Done · ${scan.event.filesAdded} added` : "");
  const remove = async () => {
    if (!window.confirm(`Remove ${props.lib.name}?`)) return;
    if (busy) return;
    setBusy(true);
    setErr("");
    try {
      const res: { error?: string } = await api(`/libraries/${props.lib.id}`, { method: "DELETE" });
      if (res.error) { setErr(res.error); toast(res.error, "error"); return; }
    } catch {
      setErr("Failed to remove library.");
      toast("Failed to remove library.", "error");
      return;
    } finally {
      setBusy(false);
    }
    toast(`Library "${props.lib.name}" removed`);
    props.onRemoved();
  };
  return (
    <div style={{ ...rowLine, gridTemplateColumns: wide ? "minmax(0, 1fr) auto" : "minmax(0, 1fr)" }}>
      <div style={libMain}>
        <span style={{ fontWeight: 600, fontSize: "0.92rem", flexShrink: 0 }}>{props.lib.name}</span>
        <span style={badge}>{props.lib.type}</span>
        <span style={libPath} title={props.lib.path}>{props.lib.path}</span>
        {line && <span style={{ fontSize: "0.78rem", color: scan.event?.status === "error" ? c.danger : c.muted, flexShrink: 0 }}>{line}</span>}
        {err && <span style={{ fontSize: "0.78rem", color: c.danger, flexShrink: 0 }}>{err}</span>}
      </div>
      <span style={rowActions}>
        <button className="press" style={ghostBtn} disabled={scan.scanning} onClick={() => { scan.start(props.lib.id); toast("Scan started"); }}>
          <IconScan size={13} />
          {scan.scanning ? "Scanning…" : "Scan"}
        </button>
        <button className="press danger-ghost" style={{ ...ghostBtn, color: c.danger }} disabled={scan.scanning || busy} onClick={remove}>Remove</button>
      </span>
    </div>
  );
}

function ScanJobs(props: { libs: Library[] }) {
  const [jobs, setJobs] = useState<JobRow[]>([]);
  const [open, setOpen] = useState(true);

  const load = () => {
    Promise.all(props.libs.map((l) =>
      api(`/libraries/${l.id}/scan/jobs?limit=10`).then((rows: JobRow[]) => rows.map((r) => ({ ...r, libraryId: l.id }))).catch(() => [])
    )).then((all) => setJobs(all.flat().sort((a, b) => (b.startedAt || b.createdAt || 0) - (a.startedAt || a.createdAt || 0))));
  };

  useEffect(() => { if (open && props.libs.length) load(); }, [open, props.libs]);

  const nameOf = (id: number) => props.libs.find((l) => l.id === id)?.name || `#${id}`;

  return (
    <section className="admin-panel" style={sectionPanel}>
      <div style={panelHead}>
        <h3 style={headTitle}>Scan Jobs</h3>
        <button className="press" style={ghostBtn} onClick={() => { setOpen(true); load(); }}>Refresh</button>
      </div>
      {jobs.length === 0 ? (
        <p style={muted}>{open ? "No scan jobs yet." : "Scan jobs appear here after the first scan."}</p>
      ) : (
        <FadeScroll>
          <table style={table}>
            <thead>
              <tr>
                <th style={thA}>Library</th>
                <th style={thA}>Status</th>
                <th style={thNum}>Seen</th>
                <th style={thNum}>Added</th>
                <th style={thNum}>Updated</th>
                <th style={thNum}>Changed</th>
                <th style={thNum}>Started</th>
                <th style={thNum}>Finished</th>
                <th style={thA}>Error</th>
              </tr>
            </thead>
            <tbody>
              {jobs.slice(0, 30).map((j) => {
                const tone = j.status === "done" ? c.ok : j.status === "error" ? c.danger : j.status === "running" ? c.accent : c.muted;
                return (
                  <tr key={j.id} className="row-hit">
                    <td style={{ ...tdA, color: c.text }}>{nameOf(j.libraryId)}</td>
                    <td style={tdA}><StatusTag status={j.status} tone={tone} /></td>
                    <td style={tdNum}>{j.filesSeen}</td>
                    <td style={tdNum}>{j.filesAdded}</td>
                    <td style={tdNum}>{j.filesUpdated}</td>
                    <td style={tdNum}>{j.worksChanged}</td>
                    <td style={tdNum}>{fmtRel(j.startedAt)}</td>
                    <td style={tdNum}>{j.finishedAt ? fmtRel(j.finishedAt) : "—"}</td>
                    <td style={{ ...tdA, color: c.danger, maxWidth: "16rem", overflow: "hidden", textOverflow: "ellipsis" }}>{j.error || "—"}</td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </FadeScroll>
      )}
    </section>
  );
}

function UsersSection(props: { users: AdminUser[]; onChanged: () => void }) {
  const [name, setName] = useState("");
  const [password, setPassword] = useState("");
  const [isAdmin, setIsAdmin] = useState(false);
  const [msg, setMsg] = useState("");
  const [busy, setBusy] = useState(false);

  const create = async (e: Event) => {
    e.preventDefault();
    setBusy(true); setMsg("");
    try {
      const res: { error?: string } = await api("/users", { method: "POST", body: JSON.stringify({ name, password, isAdmin }) });
      if (res.error) { setMsg(res.error); return; }
      setMsg("user created"); setName(""); setPassword(""); setIsAdmin(false);
      props.onChanged();
    } catch {
      setMsg("Failed to create user.");
    } finally {
      setBusy(false);
    }
  };

  const createOk = msg === "user created";

  return (
    <section className="admin-panel" style={sectionPanel}>
      <div style={panelHead}>
        <h3 style={headTitle}>Users</h3>
        {props.users.length > 0 && <span style={badge}>{props.users.length}</span>}
      </div>
      {props.users.length === 0 ? (
        <p style={muted}>No users.</p>
      ) : (
        <FadeScroll>
          <table style={table}>
            <thead>
              <tr>
                <th style={thA}>Name</th>
                <th style={thA}>Role</th>
                <th style={thNum}>Created</th>
                <th style={thAct}>Actions</th>
              </tr>
            </thead>
            <tbody>
              {props.users.map((u) => <UserRow key={u.id} u={u} onChanged={props.onChanged} />)}
            </tbody>
          </table>
        </FadeScroll>
      )}

      <form style={adminForm} onSubmit={create}>
        <Field label="Name">
          <input style={input} placeholder="name" value={name} onInput={(e) => setName((e.target as HTMLInputElement).value)} />
        </Field>
        <Field label="Password">
          <input style={input} type="password" placeholder="min 8 chars" value={password} onInput={(e) => setPassword((e.target as HTMLInputElement).value)} />
        </Field>
        <label style={checkLabel}>
          <input type="checkbox" checked={isAdmin} onChange={(e) => setIsAdmin((e.target as HTMLInputElement).checked)} style={{ accentColor: c.accent }} />
          admin
        </label>
        <button className="press btnp" style={primaryBtn} type="submit" disabled={busy}>{busy ? "Creating…" : "Create user"}</button>
        {msg && <p style={createOk ? formNote : formNoteErr}>{msg}</p>}
      </form>
    </section>
  );
}

function UserRow(props: { u: AdminUser; onChanged: () => void }) {
  const [confirmDel, setConfirmDel] = useState(false);
  const [resetOpen, setResetOpen] = useState(false);
  const [pass, setPass] = useState("");
  const [msg, setMsg] = useState("");
  const [busy, setBusy] = useState(false);

  const reset = async () => {
    setBusy(true); setMsg("");
    try {
      const res: { error?: string } = await api(`/users/${props.u.id}/password`, { method: "POST", body: JSON.stringify({ password: pass }) });
      if (res.error) { setMsg(res.error); return; }
      setMsg("password updated"); setResetOpen(false); setPass("");
    } catch {
      setMsg("Failed to update password.");
    } finally {
      setBusy(false);
    }
  };

  const del = async () => {
    setBusy(true);
    try {
      const res: { error?: string } = await api(`/users/${props.u.id}`, { method: "DELETE" });
      if (res.error) { setMsg(res.error); setConfirmDel(false); return; }
      props.onChanged();
    } catch {
      setMsg("Failed to delete user.");
      setConfirmDel(false);
    } finally {
      setBusy(false);
    }
  };

  const ok = msg === "password updated";

  return (
    <tr className="row-hit">
      <td style={{ ...tdA, color: c.text, fontWeight: 600 }}>{props.u.name}</td>
      <td style={tdA}>{props.u.isAdmin ? <span style={badge}>admin</span> : "—"}</td>
      <td style={tdNum}>{fmtRel(props.u.createdAtMs) || "—"}</td>
      <td style={tdAct}>
        {msg ? (
          <span style={{ ...muted, fontSize: "0.8rem", color: ok ? c.muted : c.danger }}>{msg}</span>
        ) : resetOpen ? (
          <span style={{ display: "inline-flex", gap: "0.45rem", alignItems: "center", flexWrap: "wrap" }}>
            <input style={{ ...input, padding: "0.35rem 0.6rem", fontSize: "0.85rem", width: "11rem" }} type="password" placeholder="new password" value={pass} onInput={(e) => setPass((e.target as HTMLInputElement).value)} />
            <button className="press" style={ghostBtn} type="button" disabled={busy} onClick={reset}>Save</button>
            <button className="press" style={ghostBtn} type="button" disabled={busy} onClick={() => { setResetOpen(false); setPass(""); }}>Cancel</button>
          </span>
        ) : confirmDel ? (
          <span style={{ display: "inline-flex", gap: "0.45rem", alignItems: "center" }}>
            <button className="press danger-ghost" style={{ ...ghostBtn, color: c.danger }} type="button" disabled={busy} onClick={del}>Confirm delete</button>
            <button className="press" style={ghostBtn} type="button" disabled={busy} onClick={() => setConfirmDel(false)}>Cancel</button>
          </span>
        ) : (
          <span style={{ display: "inline-flex", gap: "0.45rem", alignItems: "center" }}>
            <button className="press" style={ghostBtn} type="button" onClick={() => setResetOpen(true)}>Reset password</button>
            <button className="press" style={ghostBtn} type="button" onClick={() => setConfirmDel(true)}>Delete</button>
          </span>
        )}
      </td>
    </tr>
  );
}

type ImportPlan = {
  source: string;
  libraries: { name: string; type: string; new: boolean; works: number; editions: number; skipped: number }[];
  works: number; editions: number; files: number; progressRows: number; playlists: number;
  users: { name: string; isAdmin: boolean; exists: boolean; tempPassword?: string }[];
  warnings: string[];
};

function formatPlan(p: ImportPlan): string {
  const lines: string[] = [
    `${p.libraries.length} libraries · ${p.works} works · ${p.editions} editions · ${p.files} files · ${p.progressRows} progress rows${p.playlists > 0 ? ` · ${p.playlists} playlists` : ""}`,
  ];
  for (const l of p.libraries) {
    lines.push(`${l.new ? "+" : "="} ${l.name} (${l.type}) — ${l.works} works, ${l.editions} editions${l.skipped > 0 ? `, ${l.skipped} skipped` : ""}`);
  }
  for (const u of p.users) {
    if (u.tempPassword) lines.push(`user ${u.name}: temp password ${u.tempPassword} (reset after first login)`);
  }
  for (const w of p.warnings) {
    lines.push(`! ${w}`);
  }
  return lines.join("\n");
}

function ImportSection() {
  const [source, setSource] = useState("abs");
  const [path, setPath] = useState("");
  const [dryRun, setDryRun] = useState(true);
  const [plan, setPlan] = useState<ImportPlan | null>(null);
  const [busy, setBusy] = useState(false);
  const [confirmRun, setConfirmRun] = useState(false);
  const [msg, setMsg] = useState("");

  const run = async (dry: boolean) => {
    setBusy(true);
    setMsg("");
    try {
      const body = source === "abs" ? { dataDir: path, dryRun: dry } : { dbPath: path, dryRun: dry };
      const res = await api(`/import/${source}`, { method: "POST", body: JSON.stringify(body) });
      if (res.error) { setMsg(res.error); toast(res.error, "error"); return; }
      setPlan(res.plan);
      if (!dry) toast(`Import done — ${res.plan.works} works, ${res.plan.editions} editions`, "success");
      if (!dry) setConfirmRun(false);
    } catch {
      setMsg("Import failed — couldn't reach the server.");
      toast("Import failed — couldn't reach the server.", "error");
    } finally {
      setBusy(false);
    }
  };

  return (
    <section className="admin-panel" style={sectionPanel}>
      <div style={headDesc}>
        <h3 style={headTitle}>Import</h3>
      </div>
      <p style={panelDesc}>Migrate an existing Audiobookshelf or Kavita instance. The foreign database is only read.</p>
      <div style={adminForm}>
        <Field label="Source">
          <div style={{ ...selectWrap, width: "100%" }}>
            <select className="pill" style={{ width: "100%" }} value={source} onChange={(e) => { setSource((e.target as HTMLSelectElement).value); setPlan(null); }} aria-label="Import source">
              <option value="abs">Audiobookshelf (data dir)</option>
              <option value="kavita">Kavita (app.db)</option>
            </select>
            <span style={selectChevron}><IconChevronDown size={12} /></span>
          </div>
        </Field>
        <Field label="Path">
          <input
            style={input}
            placeholder={source === "abs" ? "/config/dir (contains abs_database.db)" : "/path/to/app.db"}
            value={path}
            onInput={(e) => setPath((e.target as HTMLInputElement).value)}
          />
        </Field>
        <label style={checkLabel}>
          <input type="checkbox" checked={dryRun} onChange={(e) => setDryRun((e.target as HTMLInputElement).checked)} style={{ accentColor: c.accent }} />
          Dry run (plan only, no changes)
        </label>
        <div style={{ display: "flex", gap: "0.6rem", alignItems: "center", flexWrap: "wrap" }}>
          {confirmRun && !dryRun ? (
            <>
              <button className="press danger-ghost" style={{ ...ghostBtn, color: c.danger }} type="button" disabled={busy} onClick={() => run(false)}>Import now</button>
              <button className="press" style={ghostBtn} type="button" onClick={() => setConfirmRun(false)}>Cancel</button>
            </>
          ) : (
            <button className="press btnp" style={primaryBtn} type="button" disabled={busy || !path} onClick={() => (dryRun ? run(true) : setConfirmRun(true))}>
              {busy ? "Working…" : dryRun ? "Run plan" : "Import"}
            </button>
          )}
        </div>
        {msg && <p style={formNoteErr}>{msg}</p>}
      </div>

      {plan && (
        <div style={{ marginTop: "1.4rem", maxWidth: "46rem" }}>
          <p style={{ ...fieldLabel, marginBottom: "0.5rem" }}>Plan</p>
          <pre style={preBlock}>{formatPlan(plan)}</pre>
        </div>
      )}
    </section>
  );
}

function TokensSection(props: { users: AdminUser[] }) {
  const [rows, setRows] = useState<TokenRow[]>([]);
  const [label, setLabel] = useState("");
  const [issued, setIssued] = useState("");
  const [msg, setMsg] = useState("");
  const [busy, setBusy] = useState(false);

  const load = () => api("/tokens").then((r: TokenRow[]) => setRows(Array.isArray(r) ? r : [])).catch(() => setRows([]));
  useEffect(() => { load(); }, []);

  const create = async (e: Event) => {
    e.preventDefault();
    setBusy(true); setMsg("");
    try {
      const res: { token?: string; error?: string } = await api("/tokens", { method: "POST", body: JSON.stringify({ label }) });
      if (res.error) { setMsg(res.error); return; }
      setIssued(res.token || ""); setMsg(""); setLabel("");
      load();
    } catch {
      setMsg("Failed to issue token.");
    } finally {
      setBusy(false);
    }
  };

  const revoke = async (id: number) => {
    if (busy) return;
    setBusy(true); setMsg("");
    try {
      const res: { error?: string } = await api(`/tokens/${id}`, { method: "DELETE" });
      if (res.error) { setMsg(res.error); return; }
      load();
    } catch {
      setMsg("Failed to revoke token.");
    } finally {
      setBusy(false);
    }
  };

  const nameOf = (id: number) => props.users.find((u) => u.id === id)?.name || `#${id}`;

  return (
    <section className="admin-panel" style={sectionPanel}>
      <div style={panelHead}>
        <h3 style={headTitle}>Tokens</h3>
        <button className="press" style={ghostBtn} onClick={load}>Refresh</button>
      </div>
      {rows.length === 0 ? (
        <p style={muted}>No tokens yet.</p>
      ) : (
        <FadeScroll>
          <table style={table}>
            <thead>
              <tr>
                <th style={thA}>Label</th>
                <th style={thA}>User</th>
                <th style={thNum}>Created</th>
                <th style={thNum}>Last seen</th>
                <th style={thA}>Status</th>
                <th style={thAct}>Action</th>
              </tr>
            </thead>
            <tbody>
              {rows.map((t) => (
                <tr key={t.id} className="row-hit">
                  <td style={{ ...tdA, color: c.text }}>{t.label}</td>
                  <td style={tdA}>{nameOf(t.userId)}</td>
                  <td style={tdNum}>{fmtRel(t.createdAtMs) || "—"}</td>
                  <td style={tdNum}>{fmtRel(t.lastSeenAtMs) || "—"}</td>
                  <td style={tdA}><StatusTag status={t.revokedAtMs ? "revoked" : "active"} tone={t.revokedAtMs ? c.danger : c.ok} /></td>
                  <td style={tdAct}>
                    {t.revokedAtMs ? "—" : (
                      <button className="press" style={ghostBtn} type="button" disabled={busy} onClick={() => revoke(t.id)}>Revoke</button>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </FadeScroll>
      )}

      <form style={adminForm} onSubmit={create}>
        <Field label="Label">
          <input style={input} placeholder="e.g. phone" value={label} onInput={(e) => setLabel((e.target as HTMLInputElement).value)} />
        </Field>
        <button className="press btnp" style={primaryBtn} type="submit" disabled={busy}>{busy ? "Issuing…" : "Issue"}</button>
        {msg && <p style={formNoteErr}>{msg}</p>}
      </form>

      {issued && (
        <div style={{ ...adminForm, maxWidth: "none" }}>
          <p style={formNote}>Copy this token now — it will not be shown again.</p>
          <code style={{ fontFamily: mono, fontSize: "0.82rem", wordBreak: "break-all", color: c.textDim }}>{issued}</code>
          <div>
            <button className="press" style={ghostBtn} type="button" onClick={() => setIssued("")}>Dismiss</button>
          </div>
        </div>
      )}
    </section>
  );
}

function ProvidersSection() {
  const [rows, setRows] = useState<ProviderKey[]>([]);
  const [drafts, setDrafts] = useState<Record<string, string>>({});
  const [busy, setBusy] = useState(false);
  const wide = useWide();

  const load = () => api("/settings/providers").then((r: { providers: ProviderKey[] }) => setRows(r.providers || [])).catch(() => setRows([]));
  useEffect(() => { load(); }, []);

  const put = async (keys: Record<string, string>, msg: string) => {
    setBusy(true);
    try {
      const res: { error?: string } = await api("/settings/providers", { method: "PUT", body: JSON.stringify({ keys }) });
      if (res.error) { toast(res.error, "error"); return; }
      toast(msg, "success");
      setDrafts({});
      load();
    } catch {
      toast("Failed to save.", "error");
    } finally {
      setBusy(false);
    }
  };

  const save = () => {
    const keys: Record<string, string> = {};
    for (const r of rows) {
      const v = (drafts[r.name] || "").trim();
      if (v) keys[r.name] = v;
    }
    if (Object.keys(keys).length === 0) { toast("Nothing to save.", "default"); return; }
    put(keys, "Provider keys saved");
  };

  const statusFor = (r: ProviderKey): { text: string; tone: string } => {
    if (!r.keyed) return { text: "no key required", tone: c.muted };
    if (!r.configured) return { text: "not set", tone: c.danger };
    return r.fromEnv ? { text: `env ${r.masked}`, tone: c.accent } : { text: r.masked, tone: c.ok };
  };

  return (
    <section className="admin-panel" style={sectionPanel}>
      <div style={headDesc}>
        <h3 style={headTitle}>Provider Keys</h3>
      </div>
      <p style={panelDesc}>Metadata providers for matching and enrichment. Keys apply immediately — no restart.</p>
      {rows.length === 0 && <p style={muted}>Loading…</p>}
      {rows.map((r) => {
        const st = statusFor(r);
        return (
          <div key={r.name} style={{ ...rowLine, gridTemplateColumns: wide ? "minmax(0, 1fr) minmax(16rem, 24rem)" : "minmax(0, 1fr)" }}>
            <div style={provMain}>
              <span style={provLabel}>{r.label}</span>
              <span style={{ ...badge, color: st.tone, borderColor: "transparent", background: "transparent" }}>{st.text}</span>
            </div>
            {r.keyed && (
              <div style={provControls}>
                <input
                  style={{ ...input, flex: 1, minWidth: "10rem" }}
                  type="password"
                  autocomplete="off"
                  placeholder={r.configured ? "paste new key to replace" : "paste key"}
                  aria-label={`${r.label} key`}
                  value={drafts[r.name] || ""}
                  onInput={(e) => setDrafts((d) => ({ ...d, [r.name]: (e.target as HTMLInputElement).value }))}
                />
                {r.configured && !r.fromEnv && (
                  <button className="press" style={ghostBtn} type="button" disabled={busy} onClick={() => put({ [r.name]: "" }, `${r.label} key cleared`)}>Clear</button>
                )}
              </div>
            )}
          </div>
        );
      })}
      {rows.some((r) => r.keyed) && (
        <div style={adminForm}>
          <button className="press btnp" style={primaryBtn} type="button" disabled={busy} onClick={save}>Save keys</button>
        </div>
      )}
    </section>
  );
}
