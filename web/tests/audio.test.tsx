import { render } from "preact";
import { act } from "preact/test-utils";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { AudioPlayer, type AudioController } from "../src/players/audio";

let root: HTMLDivElement;
let pause: ReturnType<typeof vi.spyOn>;

beforeEach(() => {
  root = document.createElement("div");
  document.body.append(root);
  pause = vi.spyOn(HTMLMediaElement.prototype, "pause").mockImplementation(() => {});
  vi.spyOn(HTMLMediaElement.prototype, "play").mockResolvedValue();
  localStorage.clear();
});

afterEach(() => {
  act(() => render(null, root));
  root.remove();
  vi.restoreAllMocks();
});

describe("audio queue identity", () => {
  it("replaces the source when a different queue begins at the same index", () => {
    const oldSave = vi.fn();
    const newSave = vi.fn();
    act(() => render(<AudioPlayer files={[{ id: 1, title: "First", duration: 90 }]} header="First" onPos={oldSave} />, root));
    const oldAudio = root.querySelector("audio")!;
    Object.defineProperty(oldAudio, "readyState", { value: 4 });
    act(() => oldAudio.dispatchEvent(new Event("loadedmetadata")));
    oldAudio.currentTime = 25;
    act(() => oldAudio.dispatchEvent(new Event("timeupdate")));
    oldSave.mockClear();
    act(() => render(<AudioPlayer files={[{ id: 2, title: "Second", duration: 90 }]} header="Second" onPos={newSave} />, root));
    const newAudio = root.querySelector("audio")!;
    expect(newAudio.src).toContain("/stream/2");
    expect(root.querySelector<HTMLInputElement>('input[aria-label="Seek"]')!.value).toBe("0");
    expect(oldSave).toHaveBeenCalledWith(25);
    expect(newSave).not.toHaveBeenCalled();
    expect(pause.mock.instances).toContain(oldAudio);
  });

  it("keeps the current source and position for metadata-only rerenders", () => {
    const files = [{ id: 1, title: "First", duration: 90 }];
    act(() => render(<AudioPlayer files={files} header="First" />, root));
    const audio = root.querySelector("audio")!;
    audio.currentTime = 30;
    act(() => render(<AudioPlayer files={files.map((f) => ({ ...f, title: "Renamed" }))} header="Renamed" />, root));
    expect(root.querySelector("audio")).toBe(audio);
    expect(audio.currentTime).toBe(30);
  });

  it("retains a usable controller after replacing the queue", () => {
    const controllerRef = { current: null as AudioController | null };
    act(() => render(<AudioPlayer files={[{ id: 1, title: "First", duration: 90 }]} header="First" controllerRef={controllerRef} />, root));
    act(() => render(<AudioPlayer files={[{ id: 2, title: "Second", duration: 90 }]} header="Second" controllerRef={controllerRef} />, root));
    act(() => controllerRef.current!.playAt(0, 12));
    const audio = root.querySelector("audio")!;
    act(() => audio.dispatchEvent(new Event("loadedmetadata")));
    expect(audio.src).toContain("/stream/2");
    expect(audio.currentTime).toBe(12);
  });

  it("ignores queued completion events from the detached previous audio element", () => {
    const fileEnded = vi.fn();
    const queueEnded = vi.fn();
    act(() => render(<AudioPlayer files={[{ id: 1, title: "First", duration: 90 }]} header="First" onFileEnded={fileEnded} onQueueEnded={queueEnded} />, root));
    const oldAudio = root.querySelector("audio")!;
    act(() => render(<AudioPlayer files={[{ id: 2, title: "Second", duration: 90 }]} header="Second" />, root));
    act(() => oldAudio.dispatchEvent(new Event("ended")));
    expect(fileEnded).not.toHaveBeenCalled();
    expect(queueEnded).not.toHaveBeenCalled();
    expect(root.querySelector("audio")!.src).toContain("/stream/2");
  });

});

describe("audio file-relative lifecycle", () => {
  it("saves the outgoing file at its exact position before changing parts", () => {
    const onFilePos = vi.fn();
    const controllerRef = { current: null as AudioController | null };
    act(() => render(<AudioPlayer files={[{ id: 1, title: "One", duration: 40 }, { id: 2, title: "Two", duration: 60 }]} header="Book" controllerRef={controllerRef} onFilePos={onFilePos} />, root));
    const audio = root.querySelector("audio")!;
    act(() => audio.dispatchEvent(new Event("loadedmetadata")));
    audio.currentTime = 38;
    act(() => controllerRef.current!.playAt(1, 20));
    expect(onFilePos).toHaveBeenCalledWith(0, 38);
    onFilePos.mockClear();
    act(() => audio.dispatchEvent(new Event("timeupdate")));
    expect(onFilePos).not.toHaveBeenCalled();
    act(() => audio.dispatchEvent(new Event("loadedmetadata")));
    expect(audio.currentTime).toBe(20);
    act(() => audio.dispatchEvent(new Event("seeked")));
    expect(onFilePos).toHaveBeenCalledWith(1, 20);
  });
  it("saves zero on pagehide and ignores events after detachment", () => {
    const onPos = vi.fn();
    act(() => render(<AudioPlayer files={[{ id: 1, title: "One", duration: 40 }]} header="Book" onPos={onPos} />, root));
    const audio = root.querySelector("audio")!;
    act(() => audio.dispatchEvent(new Event("loadedmetadata")));
    audio.currentTime = 0;
    act(() => dispatchEvent(new Event("pagehide")));
    expect(onPos).toHaveBeenCalledWith(0);
    act(() => render(null, root));
    onPos.mockClear();
    act(() => { audio.currentTime = 20; audio.dispatchEvent(new Event("seeked")); audio.dispatchEvent(new Event("timeupdate")); });
    expect(onPos).not.toHaveBeenCalled();
  });
});
