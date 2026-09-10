import { init, registerRoutes } from "@neutron-build/core/client";
import { routes } from "virtual:neutron/routes";

registerRoutes(routes);
void init();

if (import.meta.env.PROD && "serviceWorker" in navigator) {
  addEventListener("load", () => {
    navigator.serviceWorker.register("/sw.js").catch(() => {});
  });
}
