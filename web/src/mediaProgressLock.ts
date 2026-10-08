export function mediaOperationId(): string {
  const bytes = crypto.getRandomValues(new Uint8Array(16));
  bytes[6] = (bytes[6] & 15) | 64; bytes[8] = (bytes[8] & 63) | 128;
  const hex = Array.from(bytes, n => n.toString(16).padStart(2, "0")).join("");
  return `${hex.slice(0, 8)}-${hex.slice(8, 12)}-${hex.slice(12, 16)}-${hex.slice(16, 20)}-${hex.slice(20)}`;
}
type Lease = { holder: string; expires: number };
let database: Promise<IDBDatabase> | undefined;
function openDatabase() {
  database ??= new Promise<IDBDatabase>((resolve, reject) => {
    const request = indexedDB.open("libteca-media-lock-v1", 1);
    request.onupgradeneeded = () => request.result.createObjectStore("leases");
    request.onerror = () => reject(request.error);
    request.onblocked = () => reject(new Error("Progress lock storage is blocked"));
    request.onsuccess = () => { request.result.onversionchange = () => { request.result.close(); database = undefined; }; resolve(request.result); };
  }).catch(error => { database = undefined; throw error; });
  return database;
}
async function changeLease(name: string, holder: string, mode: "acquire" | "renew" | "release") {
  const db = await openDatabase();
  return new Promise<number>((resolve, reject) => {
    const tx = db.transaction("leases", "readwrite");
    const store = tx.objectStore("leases");
    const request = store.get(name);
    let expiry = 0;
    request.onsuccess = () => {
      const current = request.result as Lease | undefined;
      const now = Date.now();
      if (mode === "release") { if (current?.holder === holder) store.delete(name); return; }
      if ((mode === "acquire" && (!current || current.expires <= now)) || (mode === "renew" && current?.holder === holder && current.expires > now)) {
        expiry = now + 60000;
        store.put({ holder, expires: expiry } satisfies Lease, name);
      }
    };
    tx.oncomplete = () => resolve(expiry);
    tx.onerror = () => reject(tx.error);
    tx.onabort = () => reject(tx.error ?? new Error("Progress lock transaction aborted"));
  });
}
export async function withMediaReplayLock<T>(name: string, action: (owns?: () => boolean) => Promise<T>): Promise<T> {
  if (navigator.locks) return navigator.locks.request(name, () => action(() => true));
  const holder = mediaOperationId();
  let expires = await changeLease(name, holder, "acquire");
  if (!expires) throw new Error("Progress replay belongs to another tab");
  let owned = true;
  const renewal = setInterval(() => {
    void changeLease(name, holder, "renew").then(value => { if (value) expires = value; else owned = false; }).catch(() => { owned = false; });
  }, 10000);
  try { return await action(() => owned && Date.now() < expires); }
  finally { owned = false; clearInterval(renewal); await changeLease(name, holder, "release").catch(() => {}); }
}
