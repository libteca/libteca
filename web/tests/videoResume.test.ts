import { afterEach, beforeEach, expect, it, vi } from "vitest";
import type { VNode } from "preact";
import { VideoPlayer } from "../src/players/video";
import { apiChecked } from "../src/api";

const hooks = vi.hoisted(() => ({ values: [] as any[], cursor: 0, effects: [] as (() => unknown)[], cleanups: [] as (() => unknown)[] }));
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

function buttonNode(node: any, label: string): VNode<any> | undefined {
  if (!node || typeof node !== "object") return;
  if (node.type === "button" && node.props?.children === label) return node;
  for (const child of [node.props?.children].flat(Infinity)) {
    const found = buttonNode(child, label);
    if (found) return found;
  }
}

const work = {
  id: 1, title: "Series", author: null, description: null, hasCover: false,
  editions: [1, 2].map((id) => ({ id, format: "video", title: `Episode ${id}`, duration: 600, position: id === 1 ? 10 : 40,
    seasonNum: 1, episodeNum: id, files: [{ id, seq: 1, duration: 600, size: 100 }], chapters: [] })),
};

const localProgress = new Map<number, { position: number; isFinished: boolean }>();
let onProgress: ((editionId: number, patch: { position: number; duration: number; finished: boolean }) => void) | undefined;

let renderCount = 0;

function renderSession(editionId: number) {
  for (const cleanup of hooks.cleanups.splice(0)) {
    try { cleanup(); } catch { /* not mounted */ }
  }
  hooks.cursor = 0;
  hooks.effects = [];
  const session = VideoPlayer({ w: work, editionId, onClose: vi.fn(), onSelectEdition: vi.fn(), localProgress, onProgress });
  if (renderCount++ === 0) hooks.values = [];
  const tree = (session.type as (props: any) => VNode)(session.props);
  for (const effect of hooks.effects.splice(0)) {
    const cleanup = effect();
    if (typeof cleanup === "function") hooks.cleanups.push(cleanup);
  }
  hooks.effects = [];
  return tree;
}

function mediaElement(tree: VNode<any>) {
  const node = videoNode(tree)!;
  const ref = node.ref as { current: any };
  ref.current = { currentTime: 0, duration: 600, paused: true, buffered: { length: 0 }, textTracks: { length: 0 } };
  return { node, element: ref.current };
}

const flush = async () => { for (let i = 0; i < 20; i++) await Promise.resolve(); };

beforeEach(() => {
  hooks.values = [];
  hooks.cleanups = [];
  hooks.cursor = 0;
  renderCount = 0;
  localProgress.clear();
  onProgress = vi.fn((editionId: number, patch: { position: number; duration: number; finished: boolean }) => {
    localProgress.set(editionId, { position: patch.position, isFinished: patch.finished });
  });
  vi.mocked(apiChecked).mockReset().mockResolvedValue(undefined);
});

afterEach(() => { vi.unstubAllGlobals(); });

it("reopens the same edition from the fresh local position, not the stale work snapshot", async () => {
  const first = renderSession(1);
  const { node: nodeA, element: elementA } = mediaElement(first);
  await flush();
  nodeA.props.onLoadedMetadata();
  expect(elementA.currentTime).toBe(10);
  elementA.currentTime = 60;
  nodeA.props.onTimeUpdate();
  nodeA.props.onPause();
  await flush();
  expect(localProgress.get(1)).toEqual({ position: 60, isFinished: false });

  const second = renderSession(1);
  const { node: nodeB, element: elementB } = mediaElement(second);
  await flush();
  nodeB.props.onLoadedMetadata();
  expect(elementB.currentTime).toBe(60);
});

it("retry after playback captures the current position before teardown", async () => {
  const tree = renderSession(1);
  const { node, element } = mediaElement(tree);
  await flush();
  node.props.onLoadedMetadata();
  element.currentTime = 60;
  node.props.onTimeUpdate();
  const booted = renderSession(1);
  const bootedNode = videoNode(booted)!;
  bootedNode.props.onError();
  const afterError = renderSession(1);
  const button = buttonNode(afterError, "Retry");
  expect(button).toBeDefined();
  button!.props.onClick();
  await flush();
  expect(localProgress.get(1)).toEqual({ position: 60, isFinished: false });
  expect(vi.mocked(apiChecked).mock.calls.map(([, init]) => JSON.parse(String(init!.body)).position)).toContain(60);
});

it("edition A to B and back to A resumes from A's own local progress", async () => {
  const treeA1 = renderSession(1);
  const { node: nodeA1, element: elementA1 } = mediaElement(treeA1);
  await flush();
  nodeA1.props.onLoadedMetadata();
  elementA1.currentTime = 25;
  nodeA1.props.onTimeUpdate();
  nodeA1.props.onPause();
  await flush();

  const treeB = renderSession(2);
  const { node: nodeB, element: elementB } = mediaElement(treeB);
  await flush();
  nodeB.props.onLoadedMetadata();
  expect(elementB.currentTime).toBe(40);

  const treeA2 = renderSession(1);
  const { node: nodeA2, element: elementA2 } = mediaElement(treeA2);
  await flush();
  nodeA2.props.onLoadedMetadata();
  expect(elementA2.currentTime).toBe(25);
});

it("keeps an explicit seek to zero through pause and teardown", async () => {
  const tree = renderSession(1);
  const { node, element } = mediaElement(tree);
  await flush();
  node.props.onLoadedMetadata();
  element.currentTime = 60;
  node.props.onTimeUpdate();
  element.currentTime = 0;
  node.props.onSeeked();
  node.props.onPause();
  await flush();
  expect(localProgress.get(1)).toEqual({ position: 0, isFinished: false });
  const sent = vi.mocked(apiChecked).mock.calls.map(([, init]) => JSON.parse(String(init!.body)).position);
  expect(sent).toContain(0);
});

