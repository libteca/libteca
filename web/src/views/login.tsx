import { useState } from "preact/hooks";
import { errStyle, input, loginCard, loginTitle, loginWrap, primaryBtn } from "../styles";

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
    if (!data.token) { setErr("invalid credentials"); return; }
    localStorage.setItem("libteca-token", data.token);
    props.onLogin();
  };
  return (
    <div style={loginWrap}>
      <form style={loginCard} onSubmit={submit}>
        <h1 style={loginTitle}>libteca</h1>
        <input style={input} placeholder="username" value={name} onInput={(e) => setName((e.target as HTMLInputElement).value)} />
        <input style={input} type="password" placeholder="password" value={pass} onInput={(e) => setPass((e.target as HTMLInputElement).value)} />
        {err && <p style={errStyle}>{err}</p>}
        <button style={primaryBtn} type="submit">Sign in</button>
      </form>
    </div>
  );
}
