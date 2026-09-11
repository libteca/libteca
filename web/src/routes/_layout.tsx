import type { ComponentChildren } from "preact";

const BASE_CSS = `<style>
:root { --ease: cubic-bezier(0.22, 1, 0.36, 1); color-scheme: dark; }
html, body { margin: 0; background: #0c0d0f; }
body { -webkit-tap-highlight-color: transparent; }
a { color: inherit; }
::selection { background: rgba(10, 132, 255, 0.22); }
::placeholder { color: #6f7278; opacity: 1; }
mark { background: rgba(10, 132, 255, 0.24); color: inherit; border-radius: 3px; padding: 0 0.06em; }
select option { background: #141518; color: #f5f5f7; }
video::cue { background: rgba(0,0,0,0.7); }

html { scrollbar-width: thin; scrollbar-color: #2a2d33 transparent; }
::-webkit-scrollbar { width: 8px; height: 8px; }
::-webkit-scrollbar-thumb { background: #2a2d33; border-radius: 999px; }
::-webkit-scrollbar-thumb:hover { background: #3a3d45; }
::-webkit-scrollbar-track, ::-webkit-scrollbar-corner { background: transparent; }
.rail-x { scrollbar-width: none; }
.rail-x::-webkit-scrollbar { display: none; }

button, a, input, select { outline: none; }
button:focus-visible, a:focus-visible, input:focus-visible, select:focus-visible, [tabindex]:focus-visible {
  outline: 2px solid #0a84ff;
  outline-offset: 2px;
}

@keyframes libteca-fade { from { opacity: 0; } to { opacity: 1; } }
@keyframes libteca-enter { from { opacity: 0; transform: translateY(6px); } to { opacity: 1; transform: none; } }
@keyframes libteca-slide-up { from { opacity: 0; transform: translateY(14px); } to { opacity: 1; transform: none; } }
@keyframes libteca-pop { from { opacity: 0; transform: scale(0.96); } to { opacity: 1; transform: none; } }
@keyframes libteca-toast-in { from { opacity: 0; transform: translateY(0.7rem); } to { opacity: 1; transform: none; } }
@keyframes libteca-toast-out { from { opacity: 1; transform: none; } to { opacity: 0; transform: translateY(0.35rem) scale(0.98); } }
@keyframes libteca-shimmer { from { background-position: 200% 0; } to { background-position: -200% 0; } }
@keyframes libteca-spin { to { transform: rotate(360deg); } }

.anim-page { animation: libteca-enter 180ms var(--ease) both; }
.anim-fade { animation: libteca-fade 180ms ease both; }
.anim-slide-up { animation: libteca-slide-up 240ms var(--ease) both; }
.anim-pop { animation: libteca-pop 200ms var(--ease) both; }
.sk {
  background: linear-gradient(100deg, #141518 40%, #1e2025 50%, #141518 60%);
  background-size: 200% 100%;
  animation: libteca-shimmer 1.7s ease-in-out infinite;
  border-radius: 6px;
}
.spin { animation: libteca-spin 0.8s linear infinite; }
.press { transition: opacity 140ms ease; }
.press:active { opacity: 0.7; }
.press-scale { transition: transform 120ms var(--ease); }
.press-scale:active { transform: scale(0.96); }

@media (prefers-reduced-motion: reduce) {
  .anim-page, .anim-fade, .anim-slide-up, .anim-pop, .sk, .spin { animation: none; }
  .press, .press-scale { transition: none; }
  .press-scale:active { transform: none; }
}
</style>`;

export function head() {
  return [
    "<title>libteca</title>",
    '<meta name="viewport" content="width=device-width, initial-scale=1.0, viewport-fit=cover">',
    '<meta name="theme-color" content="#0c0d0f">',
    '<meta name="description" content="libteca — one media server, every client. Books, comics, film, TV, music, audiobooks and podcasts.">',
    '<link rel="manifest" href="/manifest.webmanifest">',
    '<link rel="icon" type="image/svg+xml" href="/icon.svg">',
    '<link rel="apple-touch-icon" href="/apple-touch-icon.png">',
    '<meta name="mobile-web-app-capable" content="yes">',
    '<meta name="apple-mobile-web-app-capable" content="yes">',
    '<meta name="apple-mobile-web-app-status-bar-style" content="black-translucent">',
    '<meta name="apple-mobile-web-app-title" content="libteca">',
    BASE_CSS,
  ].join("");
}

export default function Layout(props: { children?: ComponentChildren }) {
  return <main>{props.children}</main>;
}
