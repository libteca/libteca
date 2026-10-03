export type TimelineFileInput = Readonly<{ fileId: number; durationSecs: number | null }>;
export type TimelineFile = TimelineFileInput & Readonly<{ startSecs: number | null }>;
export type MediaTimeline = Readonly<{
  editionId: number;
  generation: string;
  files: readonly TimelineFile[];
  totalDurationSecs: number | null;
}>;
export type MediaPosition = Readonly<{
  editionId: number;
  generation: string;
  fileId: number;
  fileOffsetSecs: number;
  editionPositionSecs: number | null;
}>;
export type TimelineErrorCode =
  | "invalid_timeline" | "generation_mismatch" | "invalid_position"
  | "file_not_in_edition" | "position_out_of_range" | "unknown_duration";

export class MediaTimelineError extends Error {
  constructor(readonly code: TimelineErrorCode) {
    super(code);
    this.name = "MediaTimelineError";
  }
}

function fail(code: TimelineErrorCode): never {
  throw new MediaTimelineError(code);
}

function positiveID(value: unknown): value is number {
  return typeof value === "number" && Number.isSafeInteger(value) && value > 0;
}

function nonempty(value: unknown): value is string {
  return typeof value === "string" && value.trim().length > 0;
}

function seconds(value: unknown): value is number {
  return typeof value === "number" && Number.isFinite(value) && value >= 0 && value <= Number.MAX_SAFE_INTEGER;
}

function addSeconds(start: number, offset: number): number {
  const result = start + offset;
  if (!seconds(result) || (offset > 0 && result <= start)) fail("invalid_position");
  return result;
}

function requireGeneration(timeline: MediaTimeline, generation: string): void {
  if (generation !== timeline.generation) fail("generation_mismatch");
}

export function createMediaTimeline(
  editionId: number,
  generation: string,
  files: readonly TimelineFileInput[],
): MediaTimeline {
  if (!positiveID(editionId) || !nonempty(generation) || files.length === 0) fail("invalid_timeline");
  const seen = new Set<number>();
  let startSecs: number | null = 0;
  const ordered = files.map((file) => {
    if (!positiveID(file.fileId) || seen.has(file.fileId)) fail("invalid_timeline");
    if (file.durationSecs !== null && (!seconds(file.durationSecs) || file.durationSecs === 0)) fail("invalid_timeline");
    seen.add(file.fileId);
    const result = Object.freeze({ fileId: file.fileId, durationSecs: file.durationSecs, startSecs });
    if (startSecs === null || file.durationSecs === null) {
      startSecs = null;
    } else {
      try {
        startSecs = addSeconds(startSecs, file.durationSecs);
      } catch {
        fail("invalid_timeline");
      }
    }
    return result;
  });
  return Object.freeze({ editionId, generation, files: Object.freeze(ordered), totalDurationSecs: startSecs });
}

export function resolveFilePosition(
  timeline: MediaTimeline,
  generation: string,
  fileId: number,
  fileOffsetSecs: number,
): MediaPosition {
  requireGeneration(timeline, generation);
  if (!positiveID(fileId) || !seconds(fileOffsetSecs)) fail("invalid_position");
  const file = timeline.files.find((candidate) => candidate.fileId === fileId);
  if (!file) fail("file_not_in_edition");
  if (file.durationSecs !== null && fileOffsetSecs > file.durationSecs) fail("position_out_of_range");
  const editionPositionSecs = file.startSecs === null ? null : addSeconds(file.startSecs, fileOffsetSecs);
  return Object.freeze({ editionId: timeline.editionId, generation, fileId, fileOffsetSecs, editionPositionSecs });
}

export function resolveEditionPosition(
  timeline: MediaTimeline,
  generation: string,
  editionPositionSecs: number,
): MediaPosition {
  requireGeneration(timeline, generation);
  if (!seconds(editionPositionSecs)) fail("invalid_position");
  for (let index = 0; index < timeline.files.length; index++) {
    const file = timeline.files[index];
    if (file.startSecs === null) fail("unknown_duration");
    if (file.durationSecs === null) {
      if (editionPositionSecs === file.startSecs) return resolveFilePosition(timeline, generation, file.fileId, 0);
      fail("unknown_duration");
    }
    const end = addSeconds(file.startSecs, file.durationSecs);
    if (editionPositionSecs < end || (index === timeline.files.length - 1 && editionPositionSecs === end)) {
      return resolveFilePosition(timeline, generation, file.fileId, editionPositionSecs - file.startSecs);
    }
  }
  fail("position_out_of_range");
}

export type EndedFileResolution = Readonly<{
  kind: "next" | "complete";
  position: MediaPosition;
}>;

export function resolveEndedFile(timeline: MediaTimeline, generation: string, fileId: number): EndedFileResolution {
  resolveFilePosition(timeline, generation, fileId, 0);
  const index = timeline.files.findIndex((file) => file.fileId === fileId);
  const next = timeline.files[index + 1];
  if (next) return { kind: "next", position: resolveFilePosition(timeline, generation, next.fileId, 0) };
  const last = timeline.files[index];
  if (last.durationSecs === null || timeline.totalDurationSecs === null) fail("unknown_duration");
  return { kind: "complete", position: resolveFilePosition(timeline, generation, last.fileId, last.durationSecs) };
}

export type ProposedPlaybackRequest = Readonly<{
  contractVersion: 1;
  requestId: string;
  generation: string;
  fileId: number;
  fileOffsetSecs: number;
  mode: "auto" | "hls";
}>;
export type ProposedPlaybackAttempt = Readonly<{ editionId: number; request: ProposedPlaybackRequest }>;
export type ProposedPlaybackResponse = MediaPosition & Readonly<{
  contractVersion: 1;
  requestId: string;
  mediaTimeOrigin: "file-start";
  mediaStartSecs: 0;
}> & (Readonly<{ mode: "direct"; sessionId?: never }> | Readonly<{ mode: "hls"; sessionId: string }>);
export type ProposedPlaybackHTTPResult =
  | Readonly<{ status: 201; body: ProposedPlaybackResponse }>
  | Readonly<{
    status: 400 | 404 | 409 | 422 | 503;
    body: Readonly<{ error: "invalid_request" | "edition_not_found" | "transcode_unavailable" | TimelineErrorCode }>;
  }>;

function record(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function playbackRequest(value: unknown): value is ProposedPlaybackRequest {
  return record(value) && value.contractVersion === 1 && nonempty(value.requestId) && nonempty(value.generation)
    && positiveID(value.fileId) && seconds(value.fileOffsetSecs) && (value.mode === "auto" || value.mode === "hls");
}

export function proposePlaybackHTTP(
  timeline: MediaTimeline,
  editionId: number,
  body: unknown,
  negotiatedMode: "direct" | "hls",
  issuedSessionId?: string,
): ProposedPlaybackHTTPResult {
  if (editionId !== timeline.editionId) return { status: 404, body: { error: "edition_not_found" } };
  if (!playbackRequest(body)) return { status: 400, body: { error: "invalid_request" } };
  let position: MediaPosition;
  try {
    position = resolveFilePosition(timeline, body.generation, body.fileId, body.fileOffsetSecs);
  } catch (error) {
    if (!(error instanceof MediaTimelineError)) throw error;
    const status = error.code === "generation_mismatch" ? 409 : error.code === "file_not_in_edition" ? 404 : 422;
    return { status, body: { error: error.code } };
  }
  const common = { ...position, contractVersion: 1 as const, requestId: body.requestId, mediaTimeOrigin: "file-start" as const, mediaStartSecs: 0 as const };
  const mode = body.mode === "hls" ? "hls" : negotiatedMode;
  if (mode === "direct") return { status: 201, body: { ...common, mode } };
  if (!nonempty(issuedSessionId)) return { status: 503, body: { error: "transcode_unavailable" } };
  return { status: 201, body: { ...common, mode, sessionId: issuedSessionId } };
}

export function proposeHLSFallback(request: ProposedPlaybackRequest, requestId: string): ProposedPlaybackRequest {
  if (!playbackRequest(request) || !nonempty(requestId) || requestId === request.requestId) throw new Error("invalid_fallback_request");
  return Object.freeze({ ...request, requestId, mode: "hls" });
}

export type PlaybackResponseDecision =
  | Readonly<{ kind: "accept"; response: ProposedPlaybackResponse }>
  | Readonly<{ kind: "discard"; reason: "superseded" | "generation_changed" | "invalid_response" }>;

export function inspectProposedPlaybackResponse(
  timeline: MediaTimeline,
  active: ProposedPlaybackAttempt | null,
  body: unknown,
): PlaybackResponseDecision {
  if (!active) return { kind: "discard", reason: "superseded" };
  if (timeline.editionId !== active.editionId || timeline.generation !== active.request.generation) {
    return { kind: "discard", reason: "generation_changed" };
  }
  if (!record(body)) return { kind: "discard", reason: "invalid_response" };
  if (body.requestId !== active.request.requestId) return { kind: "discard", reason: "superseded" };
  if (!playbackRequest(active.request)) return { kind: "discard", reason: "invalid_response" };
  let position: MediaPosition;
  try {
    position = resolveFilePosition(timeline, active.request.generation, active.request.fileId, active.request.fileOffsetSecs);
  } catch {
    return { kind: "discard", reason: "invalid_response" };
  }
  if (body.contractVersion !== 1 || body.editionId !== position.editionId || body.generation !== position.generation
    || body.fileId !== position.fileId || body.fileOffsetSecs !== position.fileOffsetSecs
    || body.editionPositionSecs !== position.editionPositionSecs || body.mediaTimeOrigin !== "file-start" || body.mediaStartSecs !== 0
    || (body.mode !== "direct" && body.mode !== "hls") || (active.request.mode === "hls" && body.mode !== "hls")
    || (body.mode === "hls" ? !nonempty(body.sessionId) : body.sessionId !== undefined)) {
    return { kind: "discard", reason: "invalid_response" };
  }
  return { kind: "accept", response: body as ProposedPlaybackResponse };
}

export type ProposedPlaybackEvent = Readonly<{
  editionId: number;
  generation: string;
  fileId: number;
  requestId: string;
  sessionId?: string;
}>;

export function isCurrentPlaybackEvent(active: ProposedPlaybackResponse | null, event: ProposedPlaybackEvent): boolean {
  return active !== null && event.editionId === active.editionId && event.generation === active.generation
    && event.fileId === active.fileId && event.requestId === active.requestId && event.sessionId === active.sessionId;
}
