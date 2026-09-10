import { useState } from "preact/hooks";
import { c, errStyle, input, loginCard, loginSub, loginTitle, loginWrap, primaryBtn } from "../styles";

export function Login(props: { onLogin: () => void }) {
  const [name, setName] = useState("");
  const [pass, setPass] = useState("");
  const [err, setErr] = useState("");
  const submit = async (e: Event) => {
    e.preventDefault();
    const res = await fetch("/api/core/login", {
      method: "POST", headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ username: name, password: pass }),
    });
    const data = await res.json();
    if (!data.token) { setErr("Invalid credentials."); return; }
    localStorage.setItem("libteca-token", data.token);
    props.onLogin();
  };
  return (
    <div style={loginWrap}>
      <form style={loginCard} onSubmit={submit}>
        <h1 style={loginTitle}>libteca</h1>
        <p style={loginSub}>Your library.</p>
        <input
          style={{ ...input, minHeight: "42px", background: "transparent", border: "none", borderBottom: `1px solid ${c.line}`, borderRadius: 0, paddingLeft: 0 }}
          placeholder="Username"
          autoComplete="username"
          value={name}
          onInput={(e) => setName((e.target as HTMLInputElement).value)}
        />
        <input
          style={{ ...input, minHeight: "42px", background: "transparent", border: "none", borderBottom: `1px solid ${c.line}`, borderRadius: 0, paddingLeft: 0 }}
          type="password"
          placeholder="Password"
          autoComplete="current-password"
          value={pass}
          onInput={(e) => setPass((e.target as HTMLInputElement).value)}
        />
        {err && <p style={errStyle}>{err}</p>}
        <button className="press" style={{ ...primaryBtn, marginTop: "0.6rem" }} type="submit">Sign in</button>
      </form>
    </div>
  );
}
