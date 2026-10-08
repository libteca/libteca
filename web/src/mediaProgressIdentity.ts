let ownerId: string | null = null;
let token = "";
let fence = 0;
let protocolActive = false;
export function mediaProtocolActive() { return protocolActive; }
let ready: Promise<void> = Promise.resolve();
export function getMediaIdentity() { return { ownerId, token, fence }; }
export function mediaIdentityReady() { return ready; }
async function identityKey(value: string) {
  if (!crypto.subtle) {
    const seeds = [2166136261, 2246822507, 3266489909, 668265263];
    const parts = seeds.map(seed => {
      let valueHash = seed;
      for (let i = 0; i < value.length; i++) valueHash = Math.imul(valueHash ^ value.charCodeAt(i), 16777619);
      return (valueHash >>> 0).toString(16).padStart(8, "0");
    });
    return `libteca-media-owner:http:${parts.join("")}`;
  }
  const digest = await crypto.subtle.digest("SHA-256", new TextEncoder().encode(value));
  return `libteca-media-owner:${Array.from(new Uint8Array(digest), v => v.toString(16).padStart(2, "0")).join("")}`;
}
export function setMediaToken(value: string) {
  token = value; ownerId = null; fence++;
  try { protocolActive ||= localStorage.getItem("libteca-media-protocol-v1") === "1"; } catch {}
  const capturedFence = fence;
  ready = (async () => {
    if (!value) return;
    try {
      const key = await identityKey(value);
      const stored = localStorage.getItem(key);
      if (capturedFence === fence && stored && /^\d+$/.test(stored)) ownerId = stored;
    } catch {}
  })();
}
export function confirmMediaOwner(id: number) {
  ownerId = String(id);
  protocolActive = true;
  try { localStorage.setItem("libteca-media-protocol-v1", "1"); } catch {}
  const usedToken = token;
  if (usedToken) void identityKey(usedToken).then(key => { try { localStorage.setItem(key, String(id)); } catch {} }).catch(() => {});
}
