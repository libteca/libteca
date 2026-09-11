import { useState } from "preact/hooks";
import { globalCss, c, errStyle, input, loginCard, loginSub, loginTitle, loginWrap, primaryBtn } from "../styles";

export function Login(props: { onLogin: () => void }) {
  const [name, setName] = useState("");
  const [pass, setPass] = useState("");
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);
  const submit = async (e: Event) => {
    e.preventDefault();
    setBusy(true); setErr("");
    try {
      const res = await fetch("/api/core/login", {
        method: "POST", headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ username: name, password: pass }),
      });
      const data = await res.json().catch(() => ({}));
      if (!res.ok || !data.token) { setErr(data.error || (res.ok ? "Invalid credentials." : `Login failed (${res.status}).`)); return; }
      try {
        localStorage.setItem("libteca-token", data.token);
      } catch {
        setErr("Couldn't save the session — storage is unavailable.");
        return;
      }
      props.onLogin();
    } catch {
      setErr("Login failed — couldn't reach the server.");
    } finally {
      setBusy(false);
    }
  };
  return (
    <div style={{ ...loginWrap, background: "radial-gradient(1100px 720px at 30% 20%, #141c2c 0%, #0c0d0f 60%)" }}>
      <style>{globalCss}</style>
      <form style={loginCard} onSubmit={submit}>
        <h1 style={{ ...loginTitle, fontSize: "2.3rem" }}>libteca</h1>
        <p style={loginSub}>Your library</p>
        <input
          className={`login-field${err ? " login-err" : ""}`}
          style={{ ...input, minHeight: "42px" }}
          placeholder="Username"
          autoComplete="username"
          autoFocus
          value={name}
          onInput={(e) => setName((e.target as HTMLInputElement).value)}
        />
        <input
          className={`login-field${err ? " login-err" : ""}`}
          style={{ ...input, minHeight: "42px" }}
          type="password"
          placeholder="Password"
          autoComplete="current-password"
          value={pass}
          onInput={(e) => setPass((e.target as HTMLInputElement).value)}
        />
        {err && <p className="login-err-msg" style={errStyle}>{err}</p>}
        <button className="press btnp" style={{ ...primaryBtn, width: "100%", alignSelf: "stretch", marginTop: "0.6rem", boxShadow: c.accentGlow }} type="submit" disabled={busy}>{busy ? "Signing in…" : "Sign in"}</button>
      </form>
    </div>
  );
}
