import { describe, expect, it } from "vitest";
import {
  applyMediaIntent, decideMediaIntent, mediaRecoveryRecord, rebaseMediaGeneration,
  restoreMediaIntent, stageMediaIntent, transitionMediaIntent, validMediaOperation,
  type MediaDeliveryContext, type MediaIntent, type MediaIntentState,
  type MediaOperation, type MediaRecord, type MediaTimeline,
} from "../src/contracts/mediaIntent";

const position = (value: number) => ({ fileId: "file-a", fileOffset: value, position: value });
const operation = (patch: Partial<MediaOperation> = {}): MediaOperation => ({
  version: 1, operationId: "op-a", ownerId: "owner-a", target: { kind: "edition", id: "edition-a" },
  installationId: "install-a", sessionId: "play-a", intentEpoch: 1, sequence: 3,
  generation: "order-1", baseRevision: 7, intent: "heartbeat", position: position(40), ...patch,
});
const remote = (patch: Partial<MediaRecord> = {}): MediaRecord => ({
  ownerId: "owner-a", target: { kind: "edition", id: "edition-a" }, generation: "order-1",
  revision: 7, position: position(30), finished: false, deleted: false, lastWrite: null, ...patch,
});
const context = (patch: Partial<MediaDeliveryContext> = {}): MediaDeliveryContext => ({
  ownerId: "owner-a", loginGeneration: 1, generation: "order-1", online: true, replayOwner: true, ...patch,
});
const queued = (op = operation()) => transitionMediaIntent(stageMediaIntent(op), { type: "persisted", success: true });
const sending = (op = operation()) => transitionMediaIntent(queued(op), { type: "send", context: context() });
const resolve = (state: MediaIntentState, record: MediaRecord, ctx = context()) => transitionMediaIntent(state, { type: "reconcile", context: ctx, remote: record });
const committed = (op: MediaOperation, base = remote()): MediaRecord => {
  const value = applyMediaIntent(op, base);
  expect(value).not.toBeNull();
  return value!;
};
const write = (op: MediaOperation) => ({ installationId: op.installationId, sessionId: op.sessionId, intentEpoch: op.intentEpoch, sequence: op.sequence, intent: op.intent });
const restored = (state: MediaIntentState): MediaIntentState => {
  const result = restoreMediaIntent(JSON.parse(JSON.stringify(mediaRecoveryRecord(state))));
  expect(result.kind).toBe("restored");
  if (result.kind !== "restored") throw new Error(result.reason);
  return result.state;
};

const intentCases: { intent: MediaIntent; value: number; finished: boolean; deleted: boolean }[] = [
  { intent: "heartbeat", value: 40, finished: false, deleted: false },
  { intent: "seek", value: 5, finished: false, deleted: false },
  { intent: "restart", value: 0, finished: false, deleted: false },
  { intent: "finish", value: 100, finished: true, deleted: false },
  { intent: "reset", value: 0, finished: false, deleted: true },
];

describe("proposed media intent contract, without runtime integration", () => {
  it.each(intentCases)("applies $intent at its exact base without a maximum-position merge", ({ intent, value, finished, deleted }) => {
    const op = operation({ intent, position: position(value) });
    const result = committed(op);
    expect(result).toMatchObject({ revision: 8, position: position(value), finished, deleted });
    expect(result.appliedOperation).toEqual(op);
    expect(remote().position.position).toBe(30);
  });

  it.each(["seek", "restart"] as const)("uses explicit %s to reopen completed progress", (intent) => {
    const op = operation({ intent, position: position(0) });
    expect(committed(op, remote({ finished: true, position: position(100) }))).toMatchObject({ finished: false, position: position(0) });
  });

  it("rejects a backward heartbeat and retains a backward seek's full tuple", () => {
    expect(decideMediaIntent(operation({ position: position(2) }), remote())).toEqual({ kind: "conflict", reason: "backward-heartbeat" });
    const tuple = { fileId: "file-b", fileOffset: 3, position: 23 };
    expect(committed(operation({ intent: "seek", position: tuple })).position).toEqual(tuple);
  });

  it.each(["seek", "restart", "finish", "reset"] as const)("makes a stale explicit %s visible even when its value matches", (intent) => {
    const op = operation({ intent, position: position(intent === "restart" || intent === "reset" ? 0 : 40) });
    const current = remote({ revision: 8, position: op.position, finished: intent === "finish", deleted: intent === "reset" });
    expect(decideMediaIntent(op, current).kind).toBe("conflict");
    expect(applyMediaIntent(op, current)).toBeNull();
  });

  it.each(["seek", "restart", "finish", "reset"] as const)("does not let an old heartbeat undo a newer %s", (intent) => {
    const op = operation({ operationId: "newer-choice", intent, intentEpoch: 2, position: position(intent === "restart" || intent === "reset" ? 0 : 20) });
    const current = committed(op);
    expect(decideMediaIntent(operation({ position: position(90) }), current).kind).toBe("conflict");
  });

  it("does not let an older completion undo a newer restart", () => {
    const current = committed(operation({ operationId: "restart", intent: "restart", intentEpoch: 2, position: position(0) }));
    expect(decideMediaIntent(operation({ intent: "finish", position: position(100) }), current)).toEqual({ kind: "conflict", reason: "concurrent-explicit-intent" });
  });

  it.each([{ deleted: true, position: position(0) }, { finished: true, position: position(100) }])("does not reopen terminal progress with a matching-base heartbeat", (patch) => {
    expect(decideMediaIntent(operation({ position: position(100) }), remote(patch))).toEqual({ kind: "conflict", reason: "heartbeat-cannot-reopen" });
  });

  it("cannot resurrect a reset tombstone with an old creation or old positive base", () => {
    const reset = committed(operation({ operationId: "reset", intent: "reset", position: position(0) }));
    for (const baseRevision of [0, 7]) expect(decideMediaIntent(operation({ baseRevision }), reset)).toEqual({ kind: "conflict", reason: "reset-tombstone" });
    const restarted = operation({ operationId: "restart", baseRevision: 8, intentEpoch: 2, intent: "restart", position: position(0) });
    expect(committed(restarted, reset)).toMatchObject({ revision: 9, deleted: false });
  });

  it("does not recreate an absent row from a positive base", () => {
    const absent = remote({ revision: 0, position: position(0) });
    expect(decideMediaIntent(operation(), absent)).toEqual({ kind: "conflict", reason: "revision-regressed" });
    expect(committed(operation({ baseRevision: 0 }), absent).revision).toBe(1);
  });

  it("requires a fresh operation for a forward heartbeat rebase in the same session and epoch", () => {
    const previous = operation({ operationId: "previous", sequence: 2, position: position(35) });
    const current = committed(previous);
    expect(decideMediaIntent(operation(), current)).toEqual({ kind: "rebase-heartbeat", baseRevision: 8 });
    const original = queued();
    expect(resolve(original, current)).toMatchObject({ phase: "conflict", reason: "fresh-heartbeat-operation-required", operation: { baseRevision: 7 } });
    const next = operation({ operationId: "fresh-heartbeat", baseRevision: 8 });
    expect(committed(next, current).position).toEqual(position(40));
  });

  it("covers an older heartbeat only through a later same-lineage heartbeat", () => {
    const previous = operation({ sequence: 2, position: position(35) });
    const later = operation({ operationId: "later", sequence: 4, position: position(50) });
    const current = committed(later);
    expect(decideMediaIntent(previous, current)).toEqual({ kind: "covered", revision: 8 });
    expect(resolve(queued(previous), current)).toMatchObject({ phase: "settled", outcome: "covered", resolvedRevision: 8 });
    expect(current.position).toEqual(position(50));
  });

  it.each([
    { installationId: "install-b" }, { sessionId: "play-b" }, { intentEpoch: 2 },
  ])("does not merge a heartbeat across playback lineage %j", (patch) => {
    const current = committed(operation({ ...patch, operationId: "other", position: position(80) }));
    expect(decideMediaIntent(operation(), current)).toEqual({ kind: "conflict", reason: "concurrent-playback-intent" });
  });

  it("rejects sequence collisions and equal cumulative positions with incompatible tuples", () => {
    const cases = [
      operation({ operationId: "other", position: position(50) }),
      operation({ operationId: "other", sequence: 4, position: { fileId: "file-b", fileOffset: 0, position: 40 } }),
      operation({ operationId: "other", sequence: 4, position: position(35) }),
    ];
    for (const candidate of cases) expect(decideMediaIntent(operation(), committed(candidate))).toEqual({ kind: "conflict", reason: "inconsistent-heartbeat-order" });
  });

  it("rejects stale same-lineage sequence and intent epoch even at a matching revision", () => {
    const old = operation();
    expect(decideMediaIntent(old, remote({ lastWrite: write(old) }))).toEqual({ kind: "conflict", reason: "stale-sequence" });
    expect(decideMediaIntent(old, remote({ lastWrite: write(operation({ intentEpoch: 2 })) }))).toEqual({ kind: "conflict", reason: "stale-intent-epoch" });
  });

  it.each([
    { ownerId: "owner-b" }, { target: { kind: "edition" as const, id: "edition-b" } },
    { target: { kind: "podcast-episode" as const, id: "edition-a" } },
  ])("rejects a different progress scope %j", (patch) => {
    expect(decideMediaIntent(operation(), remote(patch))).toEqual({ kind: "conflict", reason: "scope-mismatch" });
  });

  it("models podcast revisions without claiming a podcast schema change", () => {
    const target = { kind: "podcast-episode" as const, id: "episode-a" };
    const op = operation({ target, intent: "seek", position: position(5) });
    expect(committed(op, remote({ target }))).toMatchObject({ target, revision: 8, position: position(5) });
  });
});

describe("proposed persistence, delivery and recovery transitions", () => {
  it.each(intentCases)("keeps offline $intent queued with its original base and exact intent", ({ intent, value }) => {
    const state = queued(operation({ intent, position: position(value) }));
    expect(transitionMediaIntent(state, { type: "send", context: context({ online: false }) })).toEqual(state);
    expect(mediaRecoveryRecord(state)?.operation).toMatchObject({ intent, baseRevision: 7, position: position(value) });
  });

  it("does not transmit before persistence or after quota/denial failure", () => {
    const staged = stageMediaIntent(operation());
    expect(transitionMediaIntent(staged, { type: "send", context: context() })).toEqual(staged);
    const failed = transitionMediaIntent(staged, { type: "persisted", success: false });
    expect(failed).toMatchObject({ phase: "persistence-failed", reason: "storage-unavailable" });
    expect(transitionMediaIntent(failed, { type: "send", context: context() })).toEqual(failed);
    expect(mediaRecoveryRecord(failed)).toBeNull();
    expect(transitionMediaIntent(failed, { type: "persisted", success: true }).phase).toBe("queued");
  });

  it("keeps a non-owner replay tab idle", () => {
    const state = queued();
    expect(transitionMediaIntent(state, { type: "send", context: context({ replayOwner: false }) })).toEqual(state);
  });

  it("requires a matching operation and committed revision in an acknowledgement", () => {
    const state = sending();
    const ack = transitionMediaIntent(state, { type: "response", context: context(), result: { operationId: "op-a", revision: 8 } });
    expect(ack).toMatchObject({ phase: "settled", outcome: "committed", resolvedRevision: 8 });
    expect(mediaRecoveryRecord(ack)).toBeNull();
    for (const result of [{ operationId: "other", revision: 8 }, { operationId: "op-a", revision: 7 }, { operationId: "op-a", revision: 9 }]) {
      expect(transitionMediaIntent(state, { type: "response", context: context(), result })).toMatchObject({ phase: "uncertain", reason: "invalid-acknowledgement" });
    }
  });

  it("retains timeouts and unreadable responses until reconciliation proves an outcome", () => {
    const uncertain = transitionMediaIntent(sending(), { type: "response", context: context(), result: "ambiguous" });
    expect(uncertain.phase).toBe("uncertain");
    expect(transitionMediaIntent(uncertain, { type: "send", context: context() })).toEqual(uncertain);
    expect(resolve(uncertain, remote())).toMatchObject({ phase: "queued", operation: { baseRevision: 7 } });
    const applied = committed(operation());
    expect(resolve(uncertain, applied)).toMatchObject({ phase: "settled", outcome: "committed", resolvedRevision: 8 });
  });

  it("allows retry only when rejection proves that no write was applied", () => {
    const retry = transitionMediaIntent(sending(), { type: "response", context: context(), result: "not-applied" });
    expect(retry).toEqual(queued());
  });

  it.each(["queued", "sending", "uncertain"] as const)("reconciles restored %s records before sending, preserving original identity and base", (phase) => {
    const original = { ...queued(operation({ intent: "seek", position: position(10) })), phase };
    const recovered = restored(original);
    expect(recovered).toMatchObject({ phase: "uncertain", operation: original.operation });
    expect(transitionMediaIntent(recovered, { type: "send", context: context() })).toEqual(recovered);
    expect(resolve(recovered, remote()).phase).toBe("queued");
    expect(resolve(recovered, committed(original.operation))).toMatchObject({ phase: "settled", outcome: "committed" });
  });

  it("does not infer a committed explicit intent from an equal-value read with no operation proof", () => {
    const op = operation({ intent: "seek", position: position(10) });
    const current = committed(op);
    delete current.appliedOperation;
    expect(resolve(restored(queued(op)), current)).toMatchObject({ phase: "conflict", reason: "concurrent-explicit-intent" });
  });

  it("does not reassert ambiguous completion after another device reopens", () => {
    const finish = operation({ intent: "finish", position: position(100) });
    const finished = committed(finish);
    const restart = operation({ operationId: "other-restart", installationId: "install-b", baseRevision: 8, intent: "restart", position: position(0) });
    const reopened = committed(restart, finished);
    expect(resolve(restored(sending(finish)), reopened)).toMatchObject({ phase: "conflict", reason: "concurrent-explicit-intent" });
  });

  it("detects reused operation IDs and inconsistent application evidence", () => {
    const op = operation();
    expect(decideMediaIntent(op, { ...committed(op), appliedOperation: { ...op, position: position(50) } })).toEqual({ kind: "conflict", reason: "operation-id-reused" });
    expect(decideMediaIntent(op, { ...committed(op), revision: 9 })).toEqual({ kind: "conflict", reason: "inconsistent-operation-proof" });
    expect(decideMediaIntent(op, { ...committed(op), position: position(50) })).toEqual({ kind: "conflict", reason: "inconsistent-operation-proof" });
    expect(decideMediaIntent(op, { ...committed(op), lastWrite: write(operation({ sessionId: "other-session" })) })).toEqual({ kind: "conflict", reason: "inconsistent-operation-proof" });
    expect(decideMediaIntent(op, { ...committed(op), lastWrite: null })).toEqual({ kind: "conflict", reason: "inconsistent-operation-proof" });
  });

  it.each([null, "owner-b"])("fences queued writes when the active owner becomes %s", (ownerId) => {
    expect(transitionMediaIntent(queued(), { type: "send", context: context({ ownerId }) })).toMatchObject({ phase: "blocked", reason: "identity-unavailable" });
    expect(resolve(restored(queued()), remote(), context({ ownerId })).phase).toBe("uncertain");
  });

  it("ignores old-login responses after account replacement and requires same-owner reconciliation", () => {
    const stopped = transitionMediaIntent(sending(), { type: "identity-changed" });
    expect(stopped).toMatchObject({ phase: "uncertain", reason: "identity-changed" });
    expect(transitionMediaIntent(stopped, { type: "response", context: context(), result: { operationId: "op-a", revision: 8 } })).toEqual(stopped);
    expect(resolve(stopped, committed(operation()), context({ ownerId: "owner-b", loginGeneration: 2 })).phase).toBe("uncertain");
    expect(resolve(stopped, committed(operation()), context({ loginGeneration: 3 }))).toMatchObject({ phase: "settled", outcome: "committed" });
  });

  it("fences token replacement for the same owner with a volatile login generation", () => {
    const state = sending();
    expect(transitionMediaIntent(state, { type: "response", context: context({ loginGeneration: 2 }), result: { operationId: "op-a", revision: 8 } })).toEqual(state);
    expect(mediaRecoveryRecord(state)).toEqual({ version: 1, operation: operation() });
    expect(restored(state).deliveryFence).toBeUndefined();
  });

  it("retains a late acknowledgement when the timeline changed during delivery", () => {
    const state = sending();
    const changed = transitionMediaIntent(state, { type: "response", context: context({ generation: "order-2" }), result: { operationId: "op-a", revision: 8 } });
    expect(changed).toMatchObject({ phase: "uncertain", reason: "generation-changed-during-delivery", operation: { generation: "order-1", baseRevision: 7 } });
    expect(mediaRecoveryRecord(changed)).not.toBeNull();
    expect(resolve(changed, committed(operation()), context({ generation: "order-2" })).phase).toBe("uncertain");
  });

  it.each(["seek", "restart", "reset"] as const)("lets a durably queued %s supersede an unsent older completion", (intent) => {
    const old = queued(operation({ intent: "finish", position: position(100) }));
    const next = queued(operation({ operationId: "next", intent, position: position(0), intentEpoch: 2, sequence: 4 }));
    expect(transitionMediaIntent(old, { type: "supersede", replacement: next })).toMatchObject({ phase: "settled", outcome: "superseded" });
    expect(transitionMediaIntent(old, { type: "supersede", replacement: { ...next, phase: "staged" } })).toEqual(old);
    expect(transitionMediaIntent(old, { type: "supersede", replacement: { ...next, phase: "persistence-failed" } })).toEqual(old);
    for (const phase of ["sending", "uncertain"] as const) {
      const possiblyApplied = { ...old, phase };
      expect(transitionMediaIntent(possiblyApplied, { type: "supersede", replacement: next })).toEqual(possiblyApplied);
    }
  });

  it("cannot supersede across account, session, target, epoch or generation", () => {
    const state = queued();
    const next = operation({ operationId: "next", intent: "restart", position: position(0), intentEpoch: 2, sequence: 4 });
    for (const patch of [{ ownerId: "owner-b" }, { sessionId: "play-b" }, { installationId: "install-b" }, { generation: "order-2" }, { intentEpoch: 1 }, { sequence: 2 }, { target: { kind: "edition" as const, id: "edition-b" } }]) {
      expect(transitionMediaIntent(state, { type: "supersede", replacement: queued({ ...next, ...patch }) })).toEqual(state);
    }
  });

  it.each([null, {}, { version: 0, operation: operation() }, { version: 1, operation: { ...operation(), version: 2 } }])("quarantines unsupported recovery records %j", (record) => {
    expect(restoreMediaIntent(record)).toEqual({ kind: "quarantine", reason: "unsupported-or-invalid-record" });
  });

  it("rejects malformed positions and revision/sequence numbers", () => {
    for (const patch of [{ baseRevision: NaN }, { baseRevision: -1 }, { baseRevision: 1.5 }, { baseRevision: Number.MAX_SAFE_INTEGER }, { sequence: -1 }, { intentEpoch: Infinity }, { position: position(Infinity) }, { position: position(-1) }, { intent: "restart" as const, position: position(3) }]) {
      expect(validMediaOperation({ ...operation(), ...patch })).toBe(false);
    }
  });

  it("copies operation values without retaining caller aliases or incidental envelope fields", () => {
    const op = operation();
    const state = stageMediaIntent({ ...op, incidental: "not-persisted" } as MediaOperation);
    op.position.position = 80;
    op.target.id = "changed";
    expect(state.operation).toEqual(operation());
    const snapshot = mediaRecoveryRecord(transitionMediaIntent(state, { type: "persisted", success: true }))!;
    snapshot.operation.position.position = 99;
    expect(state.operation.position.position).toBe(40);
    expect(snapshot.operation).not.toHaveProperty("incidental");
  });

  it("makes settled states terminal under late or repeated events", () => {
    const settled = resolve(queued(), committed(operation()));
    expect(transitionMediaIntent(settled, { type: "identity-changed" })).toEqual(settled);
    expect(transitionMediaIntent(settled, { type: "send", context: context() })).toEqual(settled);
  });
});

const before: MediaTimeline = { generation: "order-1", files: [
  { fileId: "file-a", contentIdentity: "content-a", duration: 20 },
  { fileId: "file-b", contentIdentity: "content-b", duration: 80 },
] };
const after: MediaTimeline = { generation: "order-2", files: [before.files[1], before.files[0]] };

describe("proposed generation proof", () => {
  it("blocks transmission and conflict application across a generation change", () => {
    expect(transitionMediaIntent(queued(), { type: "send", context: context({ generation: "order-2" }) })).toMatchObject({ phase: "conflict", reason: "generation-mismatch" });
    expect(decideMediaIntent(operation(), remote({ generation: "order-2" }))).toEqual({ kind: "conflict", reason: "generation-mismatch" });
  });

  it.each(["seek", "heartbeat"] as const)("maps %s by proven unchanged file identity and offset without mutating its operation", (intent) => {
    const op = operation({ intent, position: { fileId: "file-b", fileOffset: 10, position: 30 } });
    expect(rebaseMediaGeneration(op, before, after)).toEqual({ kind: "mapped", generation: "order-2", position: { fileId: "file-b", fileOffset: 10, position: 10 } });
    expect(op).toMatchObject({ baseRevision: 7, generation: "order-1", position: { position: 30 } });
  });

  it.each(["reset", "restart", "finish"] as const)("requires a new choice for global %s intent after reorder", (intent) => {
    const op = operation({ intent, position: position(intent === "finish" ? 20 : 0) });
    expect(rebaseMediaGeneration(op, before, after)).toEqual({ kind: "conflict", reason: "global-intent-needs-confirmation" });
  });

  it("does not guess offsets for replaced, deleted, or duration-changed files", () => {
    const op = operation({ position: position(10) });
    const cases = [
      { ...after, files: [after.files[0]] },
      { ...after, files: [after.files[0], { ...before.files[0], contentIdentity: "replacement" }] },
      { ...after, files: [after.files[0], { ...before.files[0], duration: 25 }] },
    ];
    for (const next of cases) expect(rebaseMediaGeneration(op, before, next).kind).toBe("conflict");
  });

  it("rejects an inconsistent tuple, missing identity, duplicate file and invalid timeline", () => {
    expect(rebaseMediaGeneration(operation({ position: { fileId: "file-b", fileOffset: 10, position: 40 } }), before, after)).toEqual({ kind: "conflict", reason: "inconsistent-position-tuple" });
    const invalid = [
      { ...after, files: [] },
      { ...after, files: [after.files[0], after.files[0]] },
      { ...after, files: [{ ...after.files[0], contentIdentity: "" }] },
      { ...after, files: [{ ...after.files[0], duration: Infinity }] },
      { ...after, files: [{ ...after.files[0], duration: 0 }] },
    ];
    for (const next of invalid) expect(rebaseMediaGeneration(operation({ position: position(10) }), before, next)).toEqual({ kind: "conflict", reason: "invalid-timeline" });
    expect(rebaseMediaGeneration(operation(), before, before)).toEqual({ kind: "conflict", reason: "invalid-generation-proof" });
  });
});
