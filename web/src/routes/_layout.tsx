import type { ComponentChildren } from "preact";

export function head() {
  return { title: "libteca" };
}

export default function Layout(props: { children?: ComponentChildren }) {
  return <main>{props.children}</main>;
}
