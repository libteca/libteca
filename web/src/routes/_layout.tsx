import type { ComponentChildren } from "preact";

export function head() {
  return [
    "<title>libteca</title>",
    '<meta name="viewport" content="width=device-width, initial-scale=1.0, viewport-fit=cover">',
    '<meta name="theme-color" content="#0c0d0f">',
    '<link rel="manifest" href="/manifest.webmanifest">',
    '<link rel="icon" type="image/svg+xml" href="/icon.svg">',
  ].join("");
}

export default function Layout(props: { children?: ComponentChildren }) {
  return <main>{props.children}</main>;
}
