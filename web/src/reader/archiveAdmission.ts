import { ReaderResourceBudget } from "./resourceBudget";

export function admitZipDirectory(buffer: ArrayBuffer, budget: ReaderResourceBudget, maxEntries = 10000) {
  const view = new DataView(buffer);
  let end = buffer.byteLength - 22;
  const minimum = Math.max(0, end - 65535);
  while (end >= minimum && view.getUint32(end, true) !== 0x06054b50) end--;
  if (end < minimum || end + 22 + view.getUint16(end + 20, true) !== buffer.byteLength) throw new Error("Invalid ZIP directory");
  const count = view.getUint16(end + 10, true);
  const size = view.getUint32(end + 12, true);
  const start = view.getUint32(end + 16, true);
  if (view.getUint16(end + 4, true) || view.getUint16(end + 6, true) || view.getUint16(end + 8, true) !== count || count === 65535 || start === 0xffffffff || size === 0xffffffff) throw new Error("Unsupported multipart or ZIP64 directory");
  if (count > maxEntries || start + size !== end) throw new Error("Archive directory exceeds supported bounds");
  const release = budget.reserve({ retainedBytes: size + count * 256 });
  try {
    const entries = new Map<string, number>();
    const decoder = new TextDecoder();
    let at = start;
    for (let i = 0; i < count; i++) {
      if (at + 46 > end || view.getUint32(at, true) !== 0x02014b50) throw new Error("Invalid ZIP entry");
      const length = view.getUint16(at + 28, true);
      const next = at + 46 + length + view.getUint16(at + 30, true) + view.getUint16(at + 32, true);
      const expanded = view.getUint32(at + 24, true);
      if (next > end || expanded === 0xffffffff || view.getUint16(at + 8, true) & 1) throw new Error("Unsupported ZIP entry");
      const name = decoder.decode(new Uint8Array(buffer, at + 46, length));
      if (!name || name.startsWith("/") || name.includes("\\") || name.split("/").includes("..") || entries.has(name)) throw new Error("Ambiguous ZIP path");
      entries.set(name, expanded);
      at = next;
    }
    if (at !== end) throw new Error("Invalid ZIP directory size");
    return { entries, release };
  } catch (error) { release(); throw error; }
}
