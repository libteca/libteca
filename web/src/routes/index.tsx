import { Island } from "@neutron-build/core/client";
import { App } from "../components/App";

export const config = { mode: "static" };

export default function Home() {
  return <Island component={App} client="load" id="libteca-app" />;
}
