import type JSZip from "jszip";
import { extractReserved, type ZipEntryLike } from "./resources";
import { ReaderResourceBudget } from "./resourceBudget";

export class BoundedEpubArchive {
  private retained: (() => void)[] = [];
  private blobs = new Map<string, Promise<Blob>>();
  private texts = new Map<string, Promise<string>>();
  private urls = new Map<string, Promise<string>>();
  private closed = false;
  private zip: JSZip | undefined;
  constructor(zip: JSZip, private sizes: Map<string, number>, private budget: ReaderResourceBudget, private signal: AbortSignal, private onFailure: (error: unknown) => void = () => {}) { this.zip = zip; }
  private name(url: string) { return decodeURIComponent(url.replace(/^\//, "").split(/[?#]/)[0]); }
  private retain(release: () => void) {
    if (this.closed || this.signal.aborted) { release(); throw new DOMException("Reader closed", "AbortError"); }
    this.retained.push(release);
  }
  getBlob(url: string, mimeType?: string): Promise<Blob> {
    const name = this.name(url);
    let pending = this.blobs.get(name);
    if (!pending) {
      pending = (async () => {
        const entry = this.zip?.file(name), size = this.sizes.get(name);
        if (!entry || size === undefined) throw new Error("Missing EPUB dependency");
        const result = await extractReserved(entry as unknown as ZipEntryLike, size, this.budget, this.signal);
        this.retain(result.release);
        const ext = name.split(".").pop()?.toLowerCase() ?? "";
        const mime: Record<string,string> = { css:"text/css",svg:"image/svg+xml",png:"image/png",jpg:"image/jpeg",jpeg:"image/jpeg",gif:"image/gif",webp:"image/webp",woff:"font/woff",woff2:"font/woff2",ttf:"font/ttf",otf:"font/otf",xhtml:"application/xhtml+xml",html:"text/html",xml:"application/xml" };
        return new Blob([result.blob], { type: mime[ext] ?? "application/octet-stream" });
      })().catch(error => { this.onFailure(error); throw error; });
      this.blobs.set(name, pending);
    }
    return pending.then(blob => mimeType ? new Blob([blob], { type: mimeType }) : blob);
  }
  getText(url: string): Promise<string> {
    const name = this.name(url);
    let pending = this.texts.get(name);
    if (!pending) {
      pending = (async () => {
        const blob = await this.getBlob(url);
        const release = this.budget.reserve({ retainedBytes: blob.size * 2 });
        try { const text = await blob.text(); this.retain(release); return text; }
        catch (error) { release(); throw error; }
      })().catch(error => { this.onFailure(error); throw error; });
      this.texts.set(name, pending);
    }
    return pending;
  }
  async request(url: string, type?: string) {
    try { return await this.requestDocument(url,type); }
    catch (error) { this.onFailure(error); throw error; }
  }
  private async requestDocument(url: string, type?: string) {
    type ??= this.name(url).split(".").pop();
    if (type === "blob") return this.getBlob(url);
    const text = await this.getText(url);
    if (["xml", "opf", "ncx", "xhtml", "html", "htm", "svg"].includes(type ?? "")) {
      if (/<!ENTITY|<!DOCTYPE[^>]*(?:\[|SYSTEM|PUBLIC)/i.test(text)) throw new Error("EPUB document entity declarations are unsupported");
      const release = this.budget.reserve({ retainedBytes: text.length * 2, documentNodes: text.length + 16 });
      try {
        const document = new DOMParser().parseFromString(text, type === "html" || type === "htm" ? "text/html" : "application/xml");
        if (document.querySelector("parsererror")) throw new Error("Invalid EPUB document");
        this.retain(release);
        return document;
      } catch (error) { release(); throw error; }
    }
    if (type === "json") throw new Error("JSON EPUB dependencies are unsupported");
    return text;
  }
  createUrl(url: string, options?: { base64?: boolean }) {
    if (options?.base64) return Promise.reject(new Error("Base64 EPUB resources are unsupported"));
    let pending = this.urls.get(url);
    if (!pending) {
      pending = this.getBlob(url).then(blob => {
        if (this.closed) throw new DOMException("Reader closed", "AbortError");
        return URL.createObjectURL(blob);
      });
      this.urls.set(url, pending);
    }
    return pending;
  }
  revokeUrl(url: string) { void this.urls.get(url)?.then(value => URL.revokeObjectURL(value)); this.urls.delete(url); }
  reserveRetained(bytes: number) { this.retain(this.budget.reserve({ retainedBytes: bytes })); }
  destroy() {
    if (this.closed) return;
    this.closed = true;
    for (const url of this.urls.values()) void url.then(value => URL.revokeObjectURL(value)).catch(() => {});
    this.urls.clear(); this.texts.clear(); this.blobs.clear(); this.zip = undefined; this.sizes.clear();
    for (const release of this.retained.splice(0)) release();
  }
}
