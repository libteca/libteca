import JSZip from "jszip";
import { describe, expect, it, vi } from "vitest";
import { admitZipDirectory } from "../src/reader/archiveAdmission";
import { BoundedEpubArchive } from "../src/reader/epubArchive";
import { ReaderResourceBudget } from "../src/reader/resourceBudget";
import { readBoundedBodyReserved } from "../src/reader/resources";

async function fixture(body = "<html><body>chapter</body></html>") {
  const zip = new JSZip(); zip.file("chapter.xhtml", body); zip.file("image.bin", new Uint8Array(100));
  return zip.generateAsync({ type: "arraybuffer", compression: "DEFLATE" });
}
describe("EPUB archive lifetime admission", () => {
  it("shares compressed and directory lifetimes with another reader and releases on close", async () => {
    const bytes = await fixture(); const budget = new ReaderResourceBudget({ retainedBytes: 4000 });
    const epub = await readBoundedBodyReserved(new Response(bytes), 4000, budget);
    const directory = admitZipDirectory(epub.buffer, budget);
    const archive = new BoundedEpubArchive(await JSZip.loadAsync(epub.buffer), directory.entries, budget, new AbortController().signal);
    await archive.getText("/chapter.xhtml");
    const held = budget.snapshot().retainedBytes;
    expect(held).toBeGreaterThan(bytes.byteLength);
    await expect(readBoundedBodyReserved(new Response(new Uint8Array(4000 - held + 1)), 4000, budget)).rejects.toThrow("retainedBytes");
    expect(budget.snapshot().retainedBytes).toBe(held);
    archive.destroy(); directory.release(); epub.release(); archive.destroy();
    expect(budget.snapshot().retainedBytes).toBe(0);
    const cbz = await readBoundedBodyReserved(new Response(bytes), 4000, budget); cbz.release();
    expect(budget.snapshot().retainedBytes).toBe(0);
  });
  it("refuses high ZIP directory count and low-byte admission before parsing", async () => {
    const zip = new JSZip(); for(let i=0;i<5;i++) zip.file(`${i}.xhtml`,"x");
    const bytes = await zip.generateAsync({type:"arraybuffer"});
    const budget = new ReaderResourceBudget({retainedBytes:1});
    expect(()=>admitZipDirectory(bytes,budget,4)).toThrow("directory");
    expect(()=>admitZipDirectory(bytes,budget,5)).toThrow("retainedBytes");
    expect(budget.snapshot().retainedBytes).toBe(0);
  });
  it("stops high expansion through the bounded dependency stream", async () => {
    const bytes=await fixture("x".repeat(10000));const budget=new ReaderResourceBudget({retainedBytes:4000});
    const directory=admitZipDirectory(bytes,budget);const failure=vi.fn();
    const archive=new BoundedEpubArchive(await JSZip.loadAsync(bytes),directory.entries,budget,new AbortController().signal,failure);
    await expect(archive.getText("/chapter.xhtml")).rejects.toThrow("retainedBytes");
    expect(failure).toHaveBeenCalled();archive.destroy();directory.release();expect(budget.snapshot().retainedBytes).toBe(0);
  });
  it("admits document work before DOMParser and refuses entity expansion", async () => {
    for(const body of ["<html><body>chapter</body></html>","<!DOCTYPE html [<!ENTITY x 'expansion'>]><html>&x;</html>"]){
      const bytes=await fixture(body);const budget=new ReaderResourceBudget({documentNodes:10});const directory=admitZipDirectory(bytes,budget);
      const parse=vi.spyOn(DOMParser.prototype,"parseFromString");const archive=new BoundedEpubArchive(await JSZip.loadAsync(bytes),directory.entries,budget,new AbortController().signal);
      await expect(archive.request("/chapter.xhtml")).rejects.toThrow();expect(parse).not.toHaveBeenCalled();parse.mockRestore();
      archive.destroy();directory.release();expect(budget.snapshot().retainedBytes).toBe(0);
    }
  });
  it("aborts pending dependency work and releases retained buffers",async()=>{
    const bytes=await fixture("x".repeat(10000));const budget=new ReaderResourceBudget();const directory=admitZipDirectory(bytes,budget);
    const controller=new AbortController();const archive=new BoundedEpubArchive(await JSZip.loadAsync(bytes),directory.entries,budget,controller.signal);
    const pending=archive.getText("/chapter.xhtml");controller.abort();archive.destroy();
    await expect(pending).rejects.toMatchObject({name:"AbortError"});directory.release();expect(budget.snapshot().retainedBytes).toBe(0);
  });
});
