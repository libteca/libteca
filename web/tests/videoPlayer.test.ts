import { afterEach, beforeEach, expect, it, vi } from "vitest";
import type { VNode } from "preact";
import { VideoPlayer } from "../src/players/video";
import { apiChecked } from "../src/api";

const hooks = vi.hoisted(() => ({ values: [] as any[], cursor: 0, effects: [] as (() => unknown)[] }));
vi.mock("preact/hooks", () => ({
  useRef: (value: unknown) => {
    const index = hooks.cursor++;
    return hooks.values[index] ?? (hooks.values[index] = { current: value });
  },
  useState: (value: unknown) => {
    const index = hooks.cursor++;
    if (!(index in hooks.values)) hooks.values[index] = typeof value === "function" ? value() : value;
    return [hooks.values[index], (next: any) => { hooks.values[index] = typeof next === "function" ? next(hooks.values[index]) : next; }];
  },
  useEffect: (effect: () => unknown) => { hooks.effects.push(effect); },
}));
vi.mock("../src/api", async (original) => ({ ...await original<object>(), apiChecked: vi.fn(), api: vi.fn(async () => ({ mode: "direct", fileId: 1 })) }));

function videoNode(node: any): VNode<any> | undefined {
  if (!node || typeof node !== "object") return;
  if (node.type === "video") return node;
  for (const child of [node.props?.children].flat(Infinity)) {
    const found = videoNode(child);
    if (found) return found;
  }
}

const work = {
  id: 1, title: "Series", author: null, description: null, hasCover: false,
  editions: [1, 2].map((id) => ({ id, format: "video", title: `Episode ${id}`, duration: 600,
    seasonNum: 1, episodeNum: id, files: [{ id, seq: 1, duration: 600, size: 100 }], chapters: [] })),
};

beforeEach(() => {
  hooks.values = [];
  hooks.effects = [];
  hooks.cursor = 0;
  vi.useFakeTimers();
  vi.stubGlobal("window", { setTimeout, clearTimeout });
  vi.stubGlobal("document", { pictureInPictureEnabled: false });
  vi.stubGlobal("navigator", {});
  vi.mocked(apiChecked).mockReset();
});

afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

it("keeps saving episode B after episode A's real ended handler is acknowledged late", async () => {
  let acknowledge!: () => void;
  vi.mocked(apiChecked).mockImplementationOnce(() => new Promise<void>((resolve) => { acknowledge = resolve; })).mockResolvedValue(undefined);
  const onSelectEdition = vi.fn();
  const render = (editionId: number) => {
    hooks.cursor = 0;
    const session = VideoPlayer({ w: work, editionId, onClose: vi.fn(), onSelectEdition });
    hooks.values = [];
    const tree = (session.type as (props: any) => VNode)(session.props);
    return videoNode(tree)!;
  };
  const a = render(1);
  a.props.onEnded();
  await Promise.resolve();
  expect(onSelectEdition).toHaveBeenCalledWith(2);
  const b = render(2);
  acknowledge();
  for (let i = 0; i < 10; i++) await Promise.resolve();
  (b.ref as { current: unknown }).current = { currentTime: 30, buffered: { length: 0 } };
  b.props.onTimeUpdate();
  b.props.onPause();
  for (let i = 0; i < 10; i++) await Promise.resolve();
  expect(vi.mocked(apiChecked).mock.calls.map(([url, init]) => ({ url, body: JSON.parse(init!.body as string) })))
    .toEqual([
      { url: "/progress/1", body: { position: 600, duration: 600, finished: true } },
      { url: "/progress/2", body: { position: 30, duration: 600, finished: false } },
    ]);
});


it("keys media sessions by edition and keeps old pause events in their original session", async () => {
  vi.mocked(apiChecked).mockResolvedValue(undefined);
  const props = { w: work, onClose: vi.fn(), onSelectEdition: vi.fn() };
  const aSession = VideoPlayer({ ...props, editionId: 1 });
  const bSession = VideoPlayer({ ...props, editionId: 2 });
  expect(aSession.key).not.toBe(bSession.key);
  const renderSession = (session: VNode<any>) => {
    hooks.cursor = 0;
    hooks.values = [];
    return videoNode((session.type as (props: any) => VNode)(session.props))!;
  };
  const a = renderSession(aSession);
  (a.ref as { current: unknown }).current = { currentTime: 590, buffered: { length: 0 } };
  a.props.onTimeUpdate();
  const b = renderSession(bSession);
  a.props.onPause();
  (b.ref as { current: unknown }).current = { currentTime: 30, buffered: { length: 0 } };
  b.props.onTimeUpdate();
  b.props.onPause();
  for (let i = 0; i < 10; i++) await Promise.resolve();
  expect(vi.mocked(apiChecked).mock.calls.map(([url, init]) => ({ url, body: JSON.parse(init!.body as string) })))
    .toEqual([
      { url: "/progress/1", body: { position: 590, duration: 600, finished: false } },
      { url: "/progress/2", body: { position: 30, duration: 600, finished: false } },
    ]);
});
