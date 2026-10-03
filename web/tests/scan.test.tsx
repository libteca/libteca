import { render } from "preact";
import { act } from "preact/test-utils";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { api } from "../src/api";
import { useScan } from "../src/scan";

vi.mock("../src/api", () => {
  const api = vi.fn();
  return { api, apiWithDeadline: api, getToken: () => "test" };
});
const request = vi.mocked(api);
let root: HTMLDivElement;
let scan: ReturnType<typeof useScan>;
let done: ReturnType<typeof vi.fn>;

class Stream extends EventTarget {
  static instances: Stream[] = [];
  onerror: (() => void) | null = null;
  close = vi.fn();
  constructor(readonly url: string) { super(); Stream.instances.push(this); }
  progress(status: string, jobId = 1) {
    this.dispatchEvent(new MessageEvent("progress", { data: JSON.stringify({ status, jobId, filesSeen: 2 }) }));
  }
}

function Harness() { scan = useScan(done); return <div>{scan.event?.status}</div>; }

beforeEach(() => {
  vi.useFakeTimers();
  Stream.instances = [];
  vi.stubGlobal("EventSource", Stream);
  request.mockReset();
  done = vi.fn();
  root = document.createElement("div");
  document.body.append(root);
  act(() => render(<Harness />, root));
});

afterEach(() => {
  act(() => render(null, root));
  root.remove();
  vi.unstubAllGlobals();
  vi.useRealTimers();
});

describe("scan lifecycle", () => {
  it("reports a rejected start without watching a job that never started", async () => {
    request.mockResolvedValue({ error: "admin required", status: 403 });
    await act(async () => { await scan.start(1); });
    expect(scan.scanning).toBe(false);
    expect(scan.event?.error).toBe("admin required");
    expect(Stream.instances).toHaveLength(0);
  });

  it("reports a network failure without remaining stuck scanning", async () => {
    request.mockRejectedValue(new Error("offline"));
    await act(async () => { await scan.start(1); });
    expect(scan.scanning).toBe(false);
    expect(scan.event?.status).toBe("error");
    expect(Stream.instances).toHaveLength(0);
  });

  it("closes an idle subscription without restarting endless polling", async () => {
    act(() => scan.attach(1));
    const stream = Stream.instances[0];
    act(() => stream.progress("idle"));
    act(() => stream.onerror?.());
    await act(async () => { await vi.advanceTimersByTimeAsync(10000); });
    expect(stream.close).toHaveBeenCalled();
    expect(scan.scanning).toBe(false);
    expect(request).not.toHaveBeenCalled();
  });

  it("ignores queued events from an older library subscription", () => {
    act(() => scan.attach(1));
    const old = Stream.instances[0];
    act(() => scan.attach(2));
    const current = Stream.instances[1];
    act(() => old.progress("done"));
    expect(current.close).not.toHaveBeenCalled();
    expect(scan.scanning).toBe(true);
    expect(done).not.toHaveBeenCalled();
    act(() => current.progress("error"));
    expect(done).toHaveBeenCalledTimes(1);
  });

  it("polls the accepted job and surfaces interrupted scans after reconnect", async () => {
    request.mockResolvedValueOnce({ error: "scan already running", status: 409, jobId: 7 });
    await act(async () => { await scan.start(1); });
    act(() => Stream.instances[0].onerror?.());
    request.mockResolvedValue({ id: 7, status: "error", error: "interrupted" });
    await act(async () => { await vi.advanceTimersByTimeAsync(3000); });
    expect(request).toHaveBeenLastCalledWith("/scan-jobs/7");
    expect(scan.scanning).toBe(false);
    expect(scan.event?.error).toBe("interrupted");
  });

  it("ignores an old start response after following a newer library", async () => {
    let resolve!: (value: unknown) => void;
    request.mockImplementationOnce(() => new Promise((res) => { resolve = res; }));
    let starting!: Promise<void>;
    act(() => { starting = scan.start(1); });
    act(() => scan.attach(2));
    await act(async () => { resolve({ status: "scanning", jobId: 1 }); await starting; });
    expect(Stream.instances).toHaveLength(1);
    expect(Stream.instances[0].url).toContain("/libraries/2/");
  });

  it("ignores a polling result after a newer subscription starts", async () => {
    let resolve!: (value: unknown) => void;
    act(() => scan.attach(1));
    act(() => Stream.instances[0].onerror?.());
    request.mockImplementationOnce(() => new Promise((res) => { resolve = res; }));
    await act(async () => { await vi.advanceTimersByTimeAsync(3000); });
    act(() => scan.attach(2));
    await act(async () => resolve([{ id: 1, status: "done" }]));
    expect(scan.scanning).toBe(true);
    expect(Stream.instances[1].close).not.toHaveBeenCalled();
    expect(done).not.toHaveBeenCalled();
  });

  it("does not accept a later job's SSE state as the requested job's result", async () => {
    request.mockResolvedValueOnce({ status: "scanning", jobId: 7 });
    await act(async () => { await scan.start(1); });
    act(() => Stream.instances[0].progress("done", 8));
    expect(done).not.toHaveBeenCalled();
    request.mockResolvedValue({ id: 7, status: "done" });
    await act(async () => { await vi.advanceTimersByTimeAsync(3000); });
    expect(request).toHaveBeenLastCalledWith("/scan-jobs/7");
    expect(done).toHaveBeenCalledTimes(1);
  });

  it("does not start a watcher after unmounting during the start request", async () => {
    let resolve!: (value: unknown) => void;
    request.mockImplementationOnce(() => new Promise((res) => { resolve = res; }));
    let starting!: Promise<void>;
    act(() => { starting = scan.start(1); });
    act(() => render(null, root));
    await act(async () => { resolve({ status: "scanning", jobId: 1 }); await starting; });
    expect(Stream.instances).toHaveLength(0);
  });


  it.each([408, 429, 500, 503])("keeps watching after an HTTP %s status failure", async (status) => {
    request.mockResolvedValueOnce({ status: "scanning", jobId: 7 });
    await act(async () => { await scan.start(1); });
    act(() => Stream.instances[0].onerror?.());
    request.mockResolvedValueOnce({ error: "temporary failure", status });
    await act(async () => { await vi.advanceTimersByTimeAsync(3000); });
    expect(scan.scanning).toBe(true);
    expect(done).not.toHaveBeenCalled();
    request.mockResolvedValue({ id: 7, status: "done" });
    await act(async () => { await vi.advanceTimersByTimeAsync(3000); });
    expect(scan.scanning).toBe(false);
    expect(done).toHaveBeenCalledTimes(1);
  });

});
