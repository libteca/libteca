import { describe, expect, it } from "vitest";
import {
  createMediaTimeline, inspectProposedPlaybackResponse, isCurrentPlaybackEvent, MediaTimelineError,
  proposeHLSFallback, proposePlaybackHTTP, resolveEditionPosition, resolveEndedFile, resolveFilePosition,
  type ProposedPlaybackRequest, type ProposedPlaybackResponse, type TimelineErrorCode,
} from "../src/contracts/mediaTimeline";

const generation = "content-order-17";
const files = [{ fileId: 41, durationSecs: 40 }, { fileId: 9, durationSecs: 60 }, { fileId: 73, durationSecs: 20 }];
const timeline = createMediaTimeline(7, generation, files);
const request: ProposedPlaybackRequest = { contractVersion: 1, requestId: "attempt-2", generation, fileId: 9, fileOffsetSecs: 25, mode: "auto" };

function expectError(action: () => unknown, code: TimelineErrorCode) {
  expect(action).toThrow(MediaTimelineError);
  expect(action).toThrow(code);
}

function responseFor(body: ProposedPlaybackRequest = request, mode: "direct" | "hls" = "direct", source = timeline): ProposedPlaybackResponse {
  const result = proposePlaybackHTTP(source, source.editionId, body, mode, "ticket-2");
  expect(result.status).toBe(201);
  if (result.status !== 201) throw new Error(result.body.error);
  return result.body;
}

function inspect(body: unknown, active = request, source = timeline) {
  return inspectProposedPlaybackResponse(source, { editionId: 7, request: active }, body);
}

describe("proposed shared multipart timeline", () => {
  it("preserves authoritative file order and immutable input-independent metadata", () => {
    expect(timeline.files).toEqual([
      { fileId: 41, durationSecs: 40, startSecs: 0 },
      { fileId: 9, durationSecs: 60, startSecs: 40 },
      { fileId: 73, durationSecs: 20, startSecs: 100 },
    ]);
    expect(timeline.totalDurationSecs).toBe(120);
    expect(Object.isFrozen(timeline)).toBe(true);
    expect(Object.isFrozen(timeline.files)).toBe(true);
    const input = [{ fileId: 1, durationSecs: 12 }];
    const copy = createMediaTimeline(1, "g1", input);
    input[0].durationSecs = 99;
    expect(copy.files[0].durationSecs).toBe(12);
    expect(Object.isFrozen(copy.files[0])).toBe(true);
  });

  it.each([
    [0, 41, 0], [39.999, 41, 39.999], [40, 9, 0], [40.001, 9, 0.001],
    [65, 9, 25], [99.999, 9, 59.999], [100, 73, 0], [100.001, 73, 0.001], [120, 73, 20],
  ])("resolves edition position %s without changing boundary ownership", (position, fileId, offset) => {
    const result = resolveEditionPosition(timeline, generation, position);
    expect(result).toMatchObject({ editionId: 7, generation, fileId, editionPositionSecs: position });
    expect(result.fileOffsetSecs).toBeCloseTo(offset, 9);
  });

  it("keeps an explicit file end distinct from a cumulative boundary seek", () => {
    expect(resolveFilePosition(timeline, generation, 41, 40)).toMatchObject({ fileId: 41, fileOffsetSecs: 40, editionPositionSecs: 40 });
    expect(resolveEditionPosition(timeline, generation, 40)).toMatchObject({ fileId: 9, fileOffsetSecs: 0, editionPositionSecs: 40 });
  });

  it("round trips positions within every file and across fractional durations", () => {
    for (const source of [timeline, createMediaTimeline(7, generation, [{ fileId: 1, durationSecs: 1.25 }, { fileId: 2, durationSecs: 2.5 }])]) {
      for (const file of source.files) {
        for (const fraction of [0, 0.1, 0.5, 0.99]) {
          const offset = file.durationSecs! * fraction;
          const position = resolveFilePosition(source, generation, file.fileId, offset);
          const restored = resolveEditionPosition(source, generation, position.editionPositionSecs!);
          expect(restored.fileId).toBe(file.fileId);
          expect(restored.fileOffsetSecs).toBeCloseTo(offset, 10);
        }
      }
    }
  });

  it.each([-1, NaN, Infinity, -Infinity, Number.MAX_SAFE_INTEGER + 1])("rejects invalid position %s rather than clamping", (value) => {
    expectError(() => resolveEditionPosition(timeline, generation, value), "invalid_position");
    expectError(() => resolveFilePosition(timeline, generation, 9, value), "invalid_position");
  });

  it("rejects positions beyond known ends and foreign membership", () => {
    expectError(() => resolveEditionPosition(timeline, generation, 120.001), "position_out_of_range");
    expectError(() => resolveFilePosition(timeline, generation, 9, 60.001), "position_out_of_range");
    expectError(() => resolveFilePosition(timeline, generation, 999, 0), "file_not_in_edition");
    expectError(() => resolveEndedFile(timeline, generation, 999), "file_not_in_edition");
  });

  it.each([0, -1, NaN, Infinity, -Infinity, Number.MAX_SAFE_INTEGER + 1, undefined, "20"])("rejects invalid duration %s", (durationSecs) => {
    expectError(() => createMediaTimeline(7, generation, [{ fileId: 1, durationSecs: durationSecs as number }]), "invalid_timeline");
  });

  it("rejects empty, duplicate, invalid identities and unrepresentable sums", () => {
    const invalid = [
      () => createMediaTimeline(7, generation, []),
      () => createMediaTimeline(7, " ", files),
      () => createMediaTimeline(0, generation, files),
      () => createMediaTimeline(7, generation, [{ fileId: 0, durationSecs: 1 }]),
      () => createMediaTimeline(7, generation, [{ fileId: 1.1, durationSecs: 1 }]),
      () => createMediaTimeline(7, generation, [{ fileId: Number.MAX_SAFE_INTEGER + 1, durationSecs: 1 }]),
      () => createMediaTimeline(7, generation, [files[0], files[0]]),
      () => createMediaTimeline(7, generation, [{ fileId: 1, durationSecs: Number.MAX_SAFE_INTEGER }, { fileId: 2, durationSecs: 2 }]),
      () => createMediaTimeline(7, generation, [{ fileId: 1, durationSecs: Number.MAX_SAFE_INTEGER }, { fileId: 2, durationSecs: 0.1 }]),
    ];
    for (const action of invalid) expectError(action, "invalid_timeline");
  });

  it("does not guess cumulative positions across unknown durations", () => {
    const source = createMediaTimeline(7, generation, [{ fileId: 41, durationSecs: 40 }, { fileId: 9, durationSecs: null }, { fileId: 73, durationSecs: 20 }]);
    expect(source.files.map((file) => file.startSecs)).toEqual([0, 40, null]);
    expect(source.totalDurationSecs).toBeNull();
    expect(resolveEditionPosition(source, generation, 39)).toMatchObject({ fileId: 41, fileOffsetSecs: 39 });
    expect(resolveEditionPosition(source, generation, 40)).toMatchObject({ fileId: 9, fileOffsetSecs: 0 });
    expectError(() => resolveEditionPosition(source, generation, 41), "unknown_duration");
    expect(resolveFilePosition(source, generation, 9, 25)).toMatchObject({ fileOffsetSecs: 25, editionPositionSecs: 65 });
    expect(resolveFilePosition(source, generation, 73, 12)).toMatchObject({ fileOffsetSecs: 12, editionPositionSecs: null });
    expect(resolveEndedFile(source, generation, 9)).toEqual({ kind: "next", position: resolveFilePosition(source, generation, 73, 0) });
    expectError(() => resolveEndedFile(source, generation, 73), "unknown_duration");
  });

  it("handles a single unknown file without inventing its end", () => {
    const source = createMediaTimeline(7, generation, [{ fileId: 9, durationSecs: null }]);
    expect(resolveEditionPosition(source, generation, 0).fileOffsetSecs).toBe(0);
    expectError(() => resolveEditionPosition(source, generation, 1), "unknown_duration");
    expectError(() => resolveEndedFile(source, generation, 9), "unknown_duration");
  });

  it("requires the generation for every resolution, including unchanged file IDs", () => {
    const changed = createMediaTimeline(7, "content-order-18", [...files].reverse());
    for (const source of [timeline, changed]) {
      expectError(() => resolveEditionPosition(source, "obsolete", 0), "generation_mismatch");
      expectError(() => resolveFilePosition(source, "obsolete", 9, 10), "generation_mismatch");
      expectError(() => resolveEndedFile(source, "obsolete", 9), "generation_mismatch");
    }
    expect(resolveFilePosition(changed, changed.generation, 9, 25).editionPositionSecs).toBe(45);
  });

  it("completes only on an explicit final-file end event, including single-file editions", () => {
    expect(resolveEndedFile(timeline, generation, 41)).toEqual({ kind: "next", position: resolveFilePosition(timeline, generation, 9, 0) });
    expect(resolveEndedFile(timeline, generation, 9)).toEqual({ kind: "next", position: resolveFilePosition(timeline, generation, 73, 0) });
    expect(resolveEndedFile(timeline, generation, 73)).toEqual({ kind: "complete", position: resolveFilePosition(timeline, generation, 73, 20) });
    expect(resolveEditionPosition(timeline, generation, 120)).not.toHaveProperty("kind");
    const single = createMediaTimeline(7, generation, [files[0]]);
    expect(resolveEndedFile(single, generation, 41)).toEqual({ kind: "complete", position: resolveFilePosition(single, generation, 41, 40) });
  });
});

describe("proposed HTTP fixtures, no shipped handler invocation", () => {
  const fixtures = [
    { name: "direct second-file resume", body: request, editionId: 7, negotiatedMode: "direct" as const, status: 201, expected: { mode: "direct", fileId: 9, fileOffsetSecs: 25, editionPositionSecs: 65, mediaStartSecs: 0 } },
    { name: "HLS second-file resume", body: request, editionId: 7, negotiatedMode: "hls" as const, status: 201, expected: { mode: "hls", fileId: 9, fileOffsetSecs: 25, editionPositionSecs: 65, sessionId: "ticket-2", mediaStartSecs: 0 } },
    { name: "forced HLS preserves selection", body: { ...request, mode: "hls" }, editionId: 7, negotiatedMode: "direct" as const, status: 201, expected: { mode: "hls", fileId: 9, fileOffsetSecs: 25 } },
    { name: "unknown edition", body: request, editionId: 8, negotiatedMode: "direct" as const, status: 404, expected: { error: "edition_not_found" } },
    { name: "foreign file", body: { ...request, fileId: 999 }, editionId: 7, negotiatedMode: "direct" as const, status: 404, expected: { error: "file_not_in_edition" } },
    { name: "stale generation", body: { ...request, generation: "content-order-16" }, editionId: 7, negotiatedMode: "direct" as const, status: 409, expected: { error: "generation_mismatch" } },
    { name: "out of range", body: { ...request, fileOffsetSecs: 61 }, editionId: 7, negotiatedMode: "direct" as const, status: 422, expected: { error: "position_out_of_range" } },
  ];

  it.each(fixtures)("$name", ({ body, editionId, negotiatedMode, status, expected }) => {
    const exchange = JSON.parse(JSON.stringify({ method: "POST", path: `/api/core/editions/${editionId}/playback-sessions`, body }));
    expect(exchange.method).toBe("POST");
    expect(exchange.path).toBe(`/api/core/editions/${editionId}/playback-sessions`);
    const result = proposePlaybackHTTP(timeline, editionId, exchange.body, negotiatedMode, "ticket-2");
    expect(result.status).toBe(status);
    expect(JSON.parse(JSON.stringify(result.body))).toMatchObject(expected);
    if (result.status === 201) {
      expect(result.body).toMatchObject({ contractVersion: 1, editionId: 7, generation, requestId: request.requestId, mediaTimeOrigin: "file-start" });
      expect(inspect(result.body, body as ProposedPlaybackRequest).kind).toBe("accept");
    }
  });

  it.each([
    null, [], {}, { ...request, contractVersion: 2 }, { ...request, fileId: "9" },
    { ...request, fileId: -1 }, { ...request, fileOffsetSecs: -1 }, { ...request, fileOffsetSecs: null },
    { ...request, fileOffsetSecs: NaN }, { ...request, fileOffsetSecs: Infinity },
    { ...request, mode: "direct" }, { ...request, requestId: " " }, { ...request, generation: "" },
  ])("rejects malformed request fixture %#", (body) => {
    expect(proposePlaybackHTTP(timeline, 7, body, "direct")).toEqual({ status: 400, body: { error: "invalid_request" } });
  });

  it("serializes the proposed ordered timeline HTTP response with an explicit version", () => {
    const exchange = JSON.parse(JSON.stringify({
      request: { method: "GET", path: "/api/core/editions/7/timeline" },
      response: { status: 200, body: { contractVersion: 1, ...timeline } },
    }));
    expect(exchange).toEqual({
      request: { method: "GET", path: "/api/core/editions/7/timeline" },
      response: { status: 200, body: {
        contractVersion: 1, editionId: 7, generation, totalDurationSecs: 120,
        files: [{ fileId: 41, durationSecs: 40, startSecs: 0 }, { fileId: 9, durationSecs: 60, startSecs: 40 }, { fileId: 73, durationSecs: 20, startSecs: 100 }],
      } },
    });
  });

  it("does not turn unavailable HLS into a first-file direct response", () => {
    expect(proposePlaybackHTTP(timeline, 7, request, "hls")).toEqual({ status: 503, body: { error: "transcode_unavailable" } });
    expect(proposePlaybackHTTP(timeline, 7, { ...request, mode: "hls" }, "direct")).toEqual({ status: 503, body: { error: "transcode_unavailable" } });
  });

  it("keeps explicit file-local playback when only the cumulative offset is unknown", () => {
    const source = createMediaTimeline(7, generation, [{ fileId: 41, durationSecs: null }, files[1]]);
    const body = responseFor(request, "hls", source);
    expect(body).toMatchObject({ fileId: 9, fileOffsetSecs: 25, editionPositionSecs: null, mediaStartSecs: 0 });
    expect(inspect(JSON.parse(JSON.stringify(body)), request, source).kind).toBe("accept");
    expect(inspect({ ...body, editionPositionSecs: 25 }, request, source).kind).toBe("discard");
  });

  it("preserves file and offset on fallback while fencing the previous direct attempt", () => {
    const direct = responseFor();
    const fallback = proposeHLSFallback(request, "attempt-3");
    expect(fallback).toEqual({ ...request, requestId: "attempt-3", mode: "hls" });
    expect(request.mode).toBe("auto");
    expect(inspect(direct, fallback)).toEqual({ kind: "discard", reason: "superseded" });
    const hls = responseFor(fallback);
    expect(hls).toMatchObject({ fileId: direct.fileId, fileOffsetSecs: direct.fileOffsetSecs, editionPositionSecs: direct.editionPositionSecs, mode: "hls", mediaStartSecs: 0 });
    expect(inspect(hls, fallback).kind).toBe("accept");
    expect(inspect({ ...hls, mode: "direct", sessionId: undefined }, fallback).kind).toBe("discard");
    expect(() => proposeHLSFallback(request, request.requestId)).toThrow("invalid_fallback_request");
    expect(() => proposeHLSFallback(request, " ")).toThrow("invalid_fallback_request");
  });

  it("preserves the latest observed direct offset when fallback happens after playback advances", () => {
    const observed = resolveFilePosition(timeline, generation, request.fileId, 33);
    const fallback = proposeHLSFallback({ ...request, fileOffsetSecs: observed.fileOffsetSecs }, "fallback-at-33");
    expect(responseFor(fallback)).toMatchObject({ mode: "hls", fileId: 9, fileOffsetSecs: 33, editionPositionSecs: 73, mediaStartSecs: 0 });
  });

  it("does not reactivate an obsolete response when its replacement fails or is cancelled", () => {
    const old = responseFor(request, "hls");
    const replacement = { ...request, requestId: "failed-replacement", fileId: 73, fileOffsetSecs: 3, mode: "hls" as const };
    const failed = proposePlaybackHTTP(timeline, 7, replacement, "hls");
    expect(failed.status).toBe(503);
    expect(inspect(failed.body, replacement).kind).toBe("discard");
    expect(inspect(old, replacement)).toEqual({ kind: "discard", reason: "superseded" });
    expect(inspectProposedPlaybackResponse(timeline, null, responseFor(replacement)).kind).toBe("discard");
    expect(isCurrentPlaybackEvent(null, old)).toBe(false);
  });

  it.each([
    { contractVersion: 2 }, { editionId: 8 }, { generation: "wrong" }, { fileId: 41 },
    { fileOffsetSecs: 0 }, { editionPositionSecs: 25 }, { mediaStartSecs: 25 },
    { mediaTimeOrigin: "edition-start" }, { mode: "unsupported" }, { mode: "hls", sessionId: "" },
    { mode: "direct", sessionId: "unwanted-ticket" },
  ])("rejects inconsistent response fixture %#", (patch) => {
    expect(inspect({ ...responseFor(), ...patch })).toEqual({ kind: "discard", reason: "invalid_response" });
  });

  it("discards cancelled, obsolete, reordered and changed-edition responses", () => {
    const body = responseFor(request, "hls");
    expect(inspectProposedPlaybackResponse(timeline, null, body)).toEqual({ kind: "discard", reason: "superseded" });
    expect(inspect(body, { ...request, requestId: "new-attempt" })).toEqual({ kind: "discard", reason: "superseded" });
    expect(inspect(body, request, createMediaTimeline(7, "content-order-18", [...files].reverse())))
      .toEqual({ kind: "discard", reason: "generation_changed" });
    expect(inspectProposedPlaybackResponse(timeline, { editionId: 8, request }, body))
      .toEqual({ kind: "discard", reason: "generation_changed" });
    expect(inspect(null)).toEqual({ kind: "discard", reason: "invalid_response" });
  });

  it("does not accept a stale HLS creation after a boundary seek or a same-file seek", () => {
    const old = responseFor(request, "hls");
    for (const seek of [{ ...request, requestId: "seek-4", fileId: 73, fileOffsetSecs: 0 }, { ...request, requestId: "seek-5", fileOffsetSecs: 10 }]) {
      const replacement = responseFor(seek, "hls");
      expect(inspect(old, seek)).toEqual({ kind: "discard", reason: "superseded" });
      expect(inspect(replacement, seek).kind).toBe("accept");
    }
  });

  it("fences detached media, manifest and completion events by file, generation, attempt and HLS session", () => {
    for (const mode of ["direct", "hls"] as const) {
      const active = responseFor(request, mode);
      const event = { editionId: 7, generation, fileId: 9, requestId: request.requestId, sessionId: active.sessionId };
      expect(isCurrentPlaybackEvent(active, event)).toBe(true);
      expect(isCurrentPlaybackEvent(null, event)).toBe(false);
      for (const patch of [{ editionId: 8 }, { generation: "previous" }, { fileId: 41 }, { requestId: "old-attempt" }, { sessionId: "old-ticket" }]) {
        expect(isCurrentPlaybackEvent(active, { ...event, ...patch })).toBe(false);
      }
    }
  });

  it("keeps legacy payloads out of the opted-in v1 path", () => {
    expect(inspect({ mode: "direct", fileId: 9 }).kind).toBe("discard");
    expect(proposePlaybackHTTP(timeline, 7, { fileId: 9 }, "direct").status).toBe(400);
  });
});
