export type MediaIntent = "heartbeat" | "seek" | "restart" | "finish" | "reset";
export type MediaTarget = { kind: "edition" | "podcast-episode"; id: string };
export type MediaPosition = { fileId: string; fileOffset: number; position: number };
export type MediaOperation = {
  version: 1;
  operationId: string;
  ownerId: string;
  target: MediaTarget;
  installationId: string;
  sessionId: string;
  intentEpoch: number;
  sequence: number;
  generation: string;
  baseRevision: number;
  intent: MediaIntent;
  position: MediaPosition;
};
export type MediaWrite = {
  installationId: string;
  sessionId: string;
  intentEpoch: number;
  sequence: number;
  intent: MediaIntent;
};
export type MediaRecord = {
  ownerId: string;
  target: MediaTarget;
  generation: string;
  revision: number;
  position: MediaPosition;
  finished: boolean;
  deleted: boolean;
  lastWrite: MediaWrite | null;
  appliedOperation?: MediaOperation;
};
export type MediaDecision =
  | { kind: "apply" }
  | { kind: "committed"; revision: number }
  | { kind: "covered"; revision: number }
  | { kind: "rebase-heartbeat"; baseRevision: number }
  | { kind: "conflict"; reason: string };
export type MediaDeliveryContext = {
  ownerId: string | null;
  loginGeneration: number;
  generation: string;
  online: boolean;
  replayOwner: boolean;
};
export type MediaIntentState = {
  operation: MediaOperation;
  phase: "staged" | "persistence-failed" | "queued" | "sending" | "uncertain" | "blocked" | "conflict" | "settled";
  reason?: string;
  outcome?: "committed" | "covered" | "superseded";
  resolvedRevision?: number;
  deliveryFence?: { ownerId: string; loginGeneration: number };
};
export type MediaIntentEvent =
  | { type: "persisted"; success: boolean }
  | { type: "send"; context: MediaDeliveryContext }
  | { type: "response"; context: MediaDeliveryContext; result: "ambiguous" | "not-applied" | { operationId: string; revision: number } }
  | { type: "identity-changed" }
  | { type: "reconcile"; context: MediaDeliveryContext; remote: MediaRecord }
  | { type: "supersede"; replacement: MediaIntentState };
export type MediaTimeline = {
  generation: string;
  files: ReadonlyArray<{ fileId: string; contentIdentity: string; duration: number }>;
};

const intents: ReadonlyArray<MediaIntent> = ["heartbeat", "seek", "restart", "finish", "reset"];
const integer = (value: unknown): value is number => typeof value === "number" && Number.isSafeInteger(value) && value >= 0;
const number = (value: unknown): value is number => typeof value === "number" && Number.isFinite(value) && value >= 0;
const string = (value: unknown): value is string => typeof value === "string" && value.length > 0;
const object = (value: unknown): value is Record<string, unknown> => value !== null && typeof value === "object" && !Array.isArray(value);
const sameTarget = (a: MediaTarget, b: MediaTarget) => a.kind === b.kind && a.id === b.id;
const samePosition = (a: MediaPosition, b: MediaPosition) => a.fileId === b.fileId && a.fileOffset === b.fileOffset && a.position === b.position;
const sameLineage = (a: MediaWrite, b: MediaWrite) => a.installationId === b.installationId && a.sessionId === b.sessionId && a.intentEpoch === b.intentEpoch;
const conflict = (reason: string): MediaDecision => ({ kind: "conflict", reason });

function validTarget(value: unknown): value is MediaTarget {
  return object(value) && (value.kind === "edition" || value.kind === "podcast-episode") && string(value.id);
}

function validPosition(value: unknown): value is MediaPosition {
  return object(value) && string(value.fileId) && number(value.fileOffset) && number(value.position) && value.fileOffset <= value.position;
}

function validWrite(value: unknown): boolean {
  return object(value) && string(value.installationId) && string(value.sessionId) && integer(value.intentEpoch) && integer(value.sequence) && intents.includes(value.intent as MediaIntent);
}

export function validMediaOperation(value: unknown): value is MediaOperation {
  return object(value) && value.version === 1 && string(value.operationId) && string(value.ownerId) && validTarget(value.target) && validWrite(value) && string(value.generation) && integer(value.baseRevision) && value.baseRevision < Number.MAX_SAFE_INTEGER && validPosition(value.position) && ((value.intent !== "restart" && value.intent !== "reset") || value.position.position === 0);
}

function validRecord(value: MediaRecord): boolean {
  return string(value.ownerId) && validTarget(value.target) && string(value.generation) && integer(value.revision) && validPosition(value.position) && typeof value.finished === "boolean" && typeof value.deleted === "boolean" && (!value.deleted || (!value.finished && value.position.position === 0)) && (value.lastWrite === null || validWrite(value.lastWrite)) && (value.revision > 0 || (!value.deleted && !value.finished && value.position.position === 0 && value.lastWrite === null && value.appliedOperation === undefined));
}

function sameOperation(a: MediaOperation, b: MediaOperation): boolean {
  return a.version === b.version && a.operationId === b.operationId && a.ownerId === b.ownerId && sameTarget(a.target, b.target) && sameLineage(a, b) && a.sequence === b.sequence && a.generation === b.generation && a.baseRevision === b.baseRevision && a.intent === b.intent && samePosition(a.position, b.position);
}

function hasExpectedValue(operation: MediaOperation, remote: MediaRecord): boolean {
  return samePosition(operation.position, remote.position) && remote.finished === (operation.intent === "finish") && remote.deleted === (operation.intent === "reset");
}

export function decideMediaIntent(operation: MediaOperation, remote: MediaRecord): MediaDecision {
  if (!validMediaOperation(operation) || !validRecord(remote)) return conflict("invalid-record");
  if (operation.ownerId !== remote.ownerId || !sameTarget(operation.target, remote.target)) return conflict("scope-mismatch");
  if (operation.generation !== remote.generation) return conflict("generation-mismatch");
  if (remote.revision < operation.baseRevision) return conflict("revision-regressed");
  if (remote.appliedOperation?.operationId === operation.operationId) {
    if (!validMediaOperation(remote.appliedOperation) || !sameOperation(operation, remote.appliedOperation)) return conflict("operation-id-reused");
    if (remote.revision !== operation.baseRevision + 1 || !hasExpectedValue(operation, remote) || !remote.lastWrite || !sameLineage(operation, remote.lastWrite) || remote.lastWrite.sequence !== operation.sequence || remote.lastWrite.intent !== operation.intent) return conflict("inconsistent-operation-proof");
    return { kind: "committed", revision: remote.revision };
  }
  if (operation.baseRevision === remote.revision) {
    if (operation.intent !== "heartbeat") return { kind: "apply" };
    if (remote.deleted || remote.finished) return conflict("heartbeat-cannot-reopen");
    if (operation.position.position < remote.position.position) return conflict("backward-heartbeat");
    if (remote.lastWrite?.installationId === operation.installationId && remote.lastWrite.sessionId === operation.sessionId && remote.lastWrite.intentEpoch > operation.intentEpoch) return conflict("stale-intent-epoch");
    if (remote.lastWrite && sameLineage(operation, remote.lastWrite) && operation.sequence <= remote.lastWrite.sequence) return conflict("stale-sequence");
    return { kind: "apply" };
  }
  if (remote.deleted) return conflict("reset-tombstone");
  if (operation.intent !== "heartbeat") return conflict("concurrent-explicit-intent");
  if (remote.finished || remote.lastWrite?.intent !== "heartbeat" || !sameLineage(operation, remote.lastWrite)) return conflict("concurrent-playback-intent");
  if (remote.lastWrite.sequence === operation.sequence && !samePosition(operation.position, remote.position)) return conflict("inconsistent-heartbeat-order");
  if (remote.position.position === operation.position.position && !samePosition(operation.position, remote.position)) return conflict("inconsistent-heartbeat-order");
  if (remote.lastWrite.sequence >= operation.sequence && remote.position.position >= operation.position.position) return { kind: "covered", revision: remote.revision };
  if (remote.lastWrite.sequence < operation.sequence && remote.position.position < operation.position.position) return { kind: "rebase-heartbeat", baseRevision: remote.revision };
  return conflict("inconsistent-heartbeat-order");
}

export function applyMediaIntent(operation: MediaOperation, remote: MediaRecord): MediaRecord | null {
  if (decideMediaIntent(operation, remote).kind !== "apply") return null;
  return {
    ...remote,
    revision: remote.revision + 1,
    position: { ...operation.position },
    finished: operation.intent === "finish",
    deleted: operation.intent === "reset",
    lastWrite: {
      installationId: operation.installationId,
      sessionId: operation.sessionId,
      intentEpoch: operation.intentEpoch,
      sequence: operation.sequence,
      intent: operation.intent,
    },
    appliedOperation: cloneOperation(operation),
  };
}

function cloneOperation(operation: MediaOperation): MediaOperation {
  return {
    version: operation.version,
    operationId: operation.operationId,
    ownerId: operation.ownerId,
    target: { kind: operation.target.kind, id: operation.target.id },
    installationId: operation.installationId,
    sessionId: operation.sessionId,
    intentEpoch: operation.intentEpoch,
    sequence: operation.sequence,
    generation: operation.generation,
    baseRevision: operation.baseRevision,
    intent: operation.intent,
    position: { fileId: operation.position.fileId, fileOffset: operation.position.fileOffset, position: operation.position.position },
  };
}

export function stageMediaIntent(operation: MediaOperation): MediaIntentState {
  if (!validMediaOperation(operation)) throw new Error("invalid-media-operation");
  return { operation: cloneOperation(operation), phase: "staged" };
}

function stateAt(state: MediaIntentState, phase: MediaIntentState["phase"], extra: Partial<MediaIntentState> = {}): MediaIntentState {
  return { operation: state.operation, phase, ...extra };
}

function allowedContext(state: MediaIntentState, context: MediaDeliveryContext): string | null {
  if (context.ownerId !== state.operation.ownerId || !integer(context.loginGeneration)) return "identity-unavailable";
  if (context.generation !== state.operation.generation) return "generation-mismatch";
  if (!context.online) return "offline";
  if (!context.replayOwner) return "replay-owned-elsewhere";
  return null;
}

export function transitionMediaIntent(state: MediaIntentState, event: MediaIntentEvent): MediaIntentState {
  if (state.phase === "settled") return state;
  switch (event.type) {
    case "persisted":
      if (state.phase !== "staged" && state.phase !== "persistence-failed") return state;
      return stateAt(state, event.success ? "queued" : "persistence-failed", event.success ? {} : { reason: "storage-unavailable" });
    case "identity-changed":
      if (state.phase === "staged" || state.phase === "persistence-failed") return state;
      return stateAt(state, state.phase === "sending" || state.phase === "uncertain" ? "uncertain" : "blocked", { reason: "identity-changed" });
    case "send": {
      if (state.phase !== "queued") return state;
      const reason = allowedContext(state, event.context);
      if (reason === "offline" || reason === "replay-owned-elsewhere") return state;
      if (reason) return stateAt(state, reason === "generation-mismatch" ? "conflict" : "blocked", { reason });
      return stateAt(state, "sending", { deliveryFence: { ownerId: state.operation.ownerId, loginGeneration: event.context.loginGeneration } });
    }
    case "response": {
      if (state.phase !== "sending" || event.context.ownerId !== state.deliveryFence?.ownerId || event.context.loginGeneration !== state.deliveryFence?.loginGeneration) return state;
      if (event.context.generation !== state.operation.generation) return stateAt(state, "uncertain", { reason: "generation-changed-during-delivery" });
      if (event.result === "ambiguous") return stateAt(state, "uncertain", { reason: "response-ambiguous" });
      if (event.result === "not-applied") return stateAt(state, "queued");
      if (event.result.operationId !== state.operation.operationId || event.result.revision !== state.operation.baseRevision + 1) return stateAt(state, "uncertain", { reason: "invalid-acknowledgement" });
      return stateAt(state, "settled", { outcome: "committed", resolvedRevision: event.result.revision });
    }
    case "reconcile": {
      if (!["queued", "uncertain", "blocked", "conflict"].includes(state.phase)) return state;
      const reason = allowedContext(state, event.context);
      if (reason) return stateAt(state, state.phase === "uncertain" ? "uncertain" : "blocked", { reason });
      const decision = decideMediaIntent(state.operation, event.remote);
      if (decision.kind === "apply") return stateAt(state, "queued");
      if (decision.kind === "committed" || decision.kind === "covered") return stateAt(state, "settled", { outcome: decision.kind, resolvedRevision: decision.revision });
      return stateAt(state, "conflict", { reason: decision.kind === "rebase-heartbeat" ? "fresh-heartbeat-operation-required" : decision.reason });
    }
    case "supersede": {
      const next = event.replacement.operation;
      if (state.phase !== "queued" || event.replacement.phase !== "queued" || !validMediaOperation(next) || next.ownerId !== state.operation.ownerId || !sameTarget(next.target, state.operation.target) || next.installationId !== state.operation.installationId || next.sessionId !== state.operation.sessionId || next.generation !== state.operation.generation || next.intentEpoch <= state.operation.intentEpoch || next.sequence <= state.operation.sequence || !["seek", "restart", "reset"].includes(next.intent) || next.operationId === state.operation.operationId) return state;
      return stateAt(state, "settled", { outcome: "superseded" });
    }
  }
}

export function mediaRecoveryRecord(state: MediaIntentState): { version: 1; operation: MediaOperation } | null {
  if (["staged", "persistence-failed", "settled"].includes(state.phase)) return null;
  return { version: 1, operation: cloneOperation(state.operation) };
}

export function restoreMediaIntent(record: unknown): { kind: "restored"; state: MediaIntentState } | { kind: "quarantine"; reason: string } {
  if (!object(record) || record.version !== 1 || !validMediaOperation(record.operation)) return { kind: "quarantine", reason: "unsupported-or-invalid-record" };
  return { kind: "restored", state: { operation: cloneOperation(record.operation), phase: "uncertain", reason: "restart-reconciliation-required" } };
}

export function rebaseMediaGeneration(operation: MediaOperation, before: MediaTimeline, after: MediaTimeline): { kind: "mapped"; generation: string; position: MediaPosition } | { kind: "conflict"; reason: string } {
  const validTimeline = (timeline: MediaTimeline) => string(timeline.generation) && timeline.files.length > 0 && new Set(timeline.files.map((file) => file.fileId)).size === timeline.files.length && timeline.files.every((file) => string(file.fileId) && string(file.contentIdentity) && number(file.duration) && file.duration > 0) && Number.isFinite(timeline.files.reduce((total, file) => total + file.duration, 0));
  if (!validMediaOperation(operation) || !validTimeline(before) || !validTimeline(after)) return { kind: "conflict", reason: "invalid-timeline" };
  if (operation.generation !== before.generation || before.generation === after.generation) return { kind: "conflict", reason: "invalid-generation-proof" };
  if (operation.intent === "reset" || operation.intent === "restart" || operation.intent === "finish") return { kind: "conflict", reason: "global-intent-needs-confirmation" };
  const previousIndex = before.files.findIndex((file) => file.fileId === operation.position.fileId);
  const nextIndex = after.files.findIndex((file) => file.fileId === operation.position.fileId);
  if (previousIndex < 0 || nextIndex < 0) return { kind: "conflict", reason: "file-identity-missing" };
  const previous = before.files[previousIndex], next = after.files[nextIndex];
  if (previous.contentIdentity !== next.contentIdentity || previous.duration !== next.duration || operation.position.fileOffset > next.duration) return { kind: "conflict", reason: "file-identity-changed" };
  const oldPosition = before.files.slice(0, previousIndex).reduce((sum, file) => sum + file.duration, 0) + operation.position.fileOffset;
  if (Math.abs(oldPosition - operation.position.position) > 1e-6) return { kind: "conflict", reason: "inconsistent-position-tuple" };
  const position = after.files.slice(0, nextIndex).reduce((sum, file) => sum + file.duration, 0) + operation.position.fileOffset;
  return { kind: "mapped", generation: after.generation, position: { fileId: next.fileId, fileOffset: operation.position.fileOffset, position } };
}
