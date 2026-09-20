# libteca repository audit — findings and implementation guide

**Repository:** [libteca/libteca](https://github.com/libteca/libteca)  
**Pinned revision:** [`470a8edcc7e406088f71ac6e91ad99480d3f1aef`](https://github.com/libteca/libteca/commit/470a8edcc7e406088f71ac6e91ad99480d3f1aef)  
**Report date:** September 19, 2026  
**Method:** Actual GitHub source-file review, code-path analysis, and isolated local reproductions.  
**Repository changes:** None. This report does not commit, push, or modify the repository.

## Scope and assurance

This is the complete record of findings established in this review, **not a guarantee that every defect in the repository has been found**. The review inspected the source/configuration files and excerpts listed in the coverage section. It was not a line-by-line review of every scanner, importer, protocol face, UI view, dependency, or deployment artifact. The repository tree and existing audit register were examined for context; old audit claims were not automatically treated as new findings or as independent verification.

The report distinguishes source-confirmed defects from design limitations, documented tradeoffs, and hardening opportunities. A source-confirmed finding means the relevant behavior is present in the pinned code. It does not mean the complete production failure/exploit was exercised. Severity is a qualitative prioritization based on plausible user impact in a multi-user deployment, not a CVSS score. Local filesystem hardening findings explicitly state their stronger attacker preconditions.

The code blocks are **reference implementations and targeted replacements**, not a single tested, ready-to-apply patch. Cross-cutting changes identify required caller, schema, storage, and protocol integration. In particular, the progress-envelope/receipt/tombstone work must ship as one coherent protocol update, with migrations and compatibility tests. Do not paste independent snippets into the repository and assume their integration has been verified.

### Verification performed and limitations

Actual code was fetched through the GitHub connector at the pinned commit. A direct clone in the local execution container failed because that container could not resolve `github.com`; that did not prevent GitHub source reads through the connector. The local Go toolchain was **1.23.2**, whereas the pinned `go.mod` requires **1.26.6**. The repository was **not built**, its complete Go/race/frontend tests were **not run**, and dependency vulnerability scans were **not run** in this environment. Prior verification claims in repository documents are not presented as tests run by this audit.

Isolated checks were executed using Python 3.13.5/SQLite 3.46.1, Node.js 22.16.0, and a standalone standard-library Go 1.23.2 program. They reproduced: the absent-row revision condition bypass; revision reuse after deletion; locator-only merge inconsistency; loss of the winning remote page on the next save; a shared-storage acknowledgement schedule; a permitted metadata subscribe/finish interleaving that panics; and a truncated PNG accepted by `DecodeConfig` but rejected by full `Decode`. These are small reproductions/transcriptions, **not** full application integration tests. Their source and observed output are included below.

## Executive summary

The most urgent group is progress integrity: account-independent persisted queues, replay operations losing their original revision, and revision reuse across deletion. The metadata subscription lifecycle also has a concrete channel-close race. Several smaller problems undermine otherwise useful protections: PDF does not use the shared progress queue; optional metadata errors disappear; a failed encoder can publish incomplete segments as ready; and backup generations do not alone coordinate database and asset consistency.

The current implementation already contains meaningful protections, including digest-based token storage and revocation reads, conditional reader writes, a serial retry queue, self-contained backup generations, rooted media opening in the inspected core path, and CI vet/race/type/build checks. The findings below focus on residual behavior and integration gaps rather than re-labeling those existing protections as absent.

**Finding count:** 0 critical, 3 high, 26 medium, 8 low; **37 total**.

| ID | Severity | Finding |
|---|---|---|
| [F01](#f01) | High | Persistent reader progress is not scoped to the authenticated account |
| [F02](#f02) | High | A persisted patch loses the revision on which it was based |
| [F03](#f03) | Medium | Conflict reconciliation forgets the winning position before the next save |
| [F04](#f04) | Medium | Locator-only conflicts can create inconsistent numeric and textual positions |
| [F05](#f05) | High | Conditional progress writes are not safe across deletion and recreation |
| [F06](#f06) | Medium | Unload beacons bypass the revision protocol and the serial queue |
| [F07](#f07) | Medium | Reader queue teardown leaves live senders and shares revision state across editions |
| [F08](#f08) | Medium | Two tabs can erase each other’s durable pending progress |
| [F09](#f09) | Medium | Malformed stored patches and permanent HTTP errors are retried indefinitely |
| [F10](#f10) | Medium | A stalled network request can wedge the single-flight progress queue |
| [F11](#f11) | Medium | The PDF reader bypasses durable, revision-checked progress delivery |
| [F12](#f12) | Medium | Explicit zero duration and empty device values cannot clear existing progress fields |
| [F13](#f13) | Medium | The full-state reading-progress convenience method no longer performs a full update |
| [F14](#f14) | Medium | Core progress writes omit the existing position policy and accept impossible positions |
| [F15](#f15) | Medium | Database failures are still disguised as not-found or a fabricated conflict baseline |
| [F16](#f16) | Low | Several progress readers leave the newly introduced revision field at zero |
| [F17](#f17) | Medium | Browser archive readers have no explicit compressed/uncompressed memory budgets |
| [F18](#f18) | Low | A failed CBZ page is rendered as permanently “loading” |
| [F19](#f19) | Medium | EPUB location caches are keyed by edition ID rather than content identity |
| [F20](#f20) | Medium | Metadata SSE subscription can send on a closed channel and lose the terminal event |
| [F21](#f21) | Medium | Optional metadata failures are silently converted into successful-looking results |
| [F22](#f22) | Medium | Chapter replacement checks stale values but updates without comparing them |
| [F23](#f23) | Medium | Provider chapters spanning file boundaries lose their continuation |
| [F24](#f24) | Medium | Cover validation accepts non-JPEG and header-only images but stores a .jpg filename |
| [F25](#f25) | Medium | Generation directories do not by themselves make the database and covers one consistent snapshot |
| [F26](#f26) | Medium | A missing cover source can be silently accepted as a successful backup |
| [F27](#f27) | Low | Backup copying still has a path-check-to-open confinement race |
| [F28](#f28) | Low | Crashed backup stages accumulate and retention deletions are not directory-synced |
| [F29](#f29) | Medium | Media URLs expose a general bearer credential rather than a narrowly scoped capability |
| [F30](#f30) | Low | The token “last seen” throttle still executes a write statement on every authenticated request |
| [F31](#f31) | Medium | A failed transcoder can make an unfinished segment appear safe to serve |
| [F32](#f32) | Medium | Transcode cleanup holds the global manager lock during slow process/filesystem operations |
| [F33](#f33) | Medium | Session-count limits do not bound transcode disk consumption |
| [F34](#f34) | Medium | Edition-level playback selection is limited to the first file |
| [F35](#f35) | Low | The browser-playable predicate excludes VP8 even in its accepted WebM branch |
| [F36](#f36) | Low | CI uses mutable action tags and lacks an explicit dependency-security gate |
| [F37](#f37) | Low | Atomic cover publication is not explicitly crash-durable |

## Suggested implementation order

**First, stop cross-account and stale replay:** F01–F08, with the F05 migration, F02 receipt protocol, and account-aware browser tests delivered together. Include F09–F16 so validation, error handling, PDF, and store read/write contracts do not bypass that protocol.

**Next, fix independent correctness defects:** metadata channel lifetime and metadata writes (F20–F24), finalized transcode readiness (F31), and VP8 classification (F35). These can largely be isolated from the progress migration.

**Then harden durability and resource behavior:** browser archive limits/cache invalidation (F17–F19), backup consistency/completeness (F25–F28), ticket scoping and activity writes (F29–F30), transcode lifecycle/storage budgets and multipart playback (F32–F34), CI protections (F36), and durable cover publication (F37). Coordinate F24/F25/F37 around one asset-publication contract.

## Detailed findings

<a id="f01"></a>

### F01 — Persistent reader progress is not scoped to the authenticated account

**Severity: High.** Source-confirmed account-isolation defect.

**Source:** [`web/src/reader/shared.tsx`](https://github.com/libteca/libteca/blob/470a8edcc7e406088f71ac6e91ad99480d3f1aef/web/src/reader/shared.tsx); [`web/src/api.ts`](https://github.com/libteca/libteca/blob/470a8edcc7e406088f71ac6e91ad99480d3f1aef/web/src/api.ts).

**Evidence and impact.** `useProgressSaver` stores pending work under `libteca-progress-queue-${editionId}`. The entry contains no user identity. `api()` attaches the *currently active* global token when a request is sent. Consequently, account A can leave an undelivered patch, sign out, and account B can open the same edition and replay A's patch into B's progress. This is cross-account state contamination on a shared browser; it is not evidence of arbitrary server-side account impersonation.

**Suggested fix.** Scope every durable operation and queue to the authenticated user and edition. Obtain the user ID from the authenticated `/me` response, not from untrusted storage. Pause admission while identity is unresolved. Destroy old queues on logout/account changes, and check that the captured identity is still current before sending. Never use the bearer token itself as a storage key.

**Implementation — replace the edition-only namespace and add an identity guard.**
```ts
export type ReaderScope = Readonly<{ userId: number; editionId: number }>;

export function progressPrefix(scope: ReaderScope): string {
  if (!Number.isSafeInteger(scope.userId) || scope.userId <= 0 ||
      !Number.isSafeInteger(scope.editionId) || scope.editionId <= 0) {
    throw new Error("Authenticated reader scope is required");
  }
  return `libteca:progress:v2:u${scope.userId}:e${scope.editionId}:`;
}

export class AccountChangedError extends Error {}

export function assertReaderAccount(
  scope: ReaderScope, currentUserId: () => number | null,
): void {
  if (currentUserId() !== scope.userId) {
    throw new AccountChangedError("Reader account changed; delivery stopped");
  }
}
```
Pass `userId` into the reader/saver from the authenticated application state and include it in the queue lifecycle dependency list. Call `assertReaderAccount` immediately before each attempt, including retries. Treat this exception as a paused/terminal condition, not an ordinary network failure. Retire the old unscoped storage entries rather than importing them into an arbitrary account. F07 and F08 complete the lifecycle and storage changes.

**Regression test.** A queues an offline patch, logs out, B logs in, and B opens the same edition. Assert that B sends none of A's operations and sees only B's server progress. Then sign back into A and verify the documented replay policy.

---

<a id="f02"></a>

### F02 — A persisted patch loses the revision on which it was based

**Severity: High.** Source-confirmed durability/protocol defect.

**Source:** [`web/src/progressQueue.ts`](https://github.com/libteca/libteca/blob/470a8edcc7e406088f71ac6e91ad99480d3f1aef/web/src/progressQueue.ts); [`web/src/reader/shared.tsx`](https://github.com/libteca/libteca/blob/470a8edcc7e406088f71ac6e91ad99480d3f1aef/web/src/reader/shared.tsx); [`web/src/reader/cbz.tsx`](https://github.com/libteca/libteca/blob/470a8edcc7e406088f71ac6e91ad99480d3f1aef/web/src/reader/cbz.tsx); [`web/src/reader/epub.tsx`](https://github.com/libteca/libteca/blob/470a8edcc7e406088f71ac6e91ad99480d3f1aef/web/src/reader/epub.tsx).

**Evidence and impact.** Persistence saves only `{...inFlight, ...pending}`. On the next reader instance, the sender takes its revision from `baseRevision`, supplied from freshly loaded progress, rather than from the persisted operation. For example: an offline page-20 patch was based on revision 3; another device reaches page 90/revision 4; reopening with revision 4 attaches that new revision to the old page-20 patch. The server accepts it without issuing the conflict on which the merge logic depends. The source comment that replay is safe under the revision protocol is therefore too broad.

**Suggested fix.** Persist a complete immutable operation, including the original base revision and an operation ID. A replay must preserve that base. Do not relabel old operations with a new server revision without reconciling their contents. Make acknowledged retries idempotent, especially for `finished`/reopen intent.

**Implementation — durable operation envelope.**
```ts
import type { ProgressPatch } from "./progressQueue";

export type ProgressOperation = Readonly<{
  version: 2;
  id: string;
  userId: number;
  editionId: number;
  baseRevision: number;
  patch: ProgressPatch;
}>;

export function newProgressOperation(
  userId: number, editionId: number, baseRevision: number,
  patch: ProgressPatch,
): ProgressOperation {
  if (!Number.isSafeInteger(baseRevision) || baseRevision < 0) {
    throw new Error("Invalid progress revision");
  }
  return {
    version: 2, id: crypto.randomUUID(), userId, editionId,
    baseRevision, patch: { ...patch },
  };
}

export function progressRequestBody(op: ProgressOperation): string {
  return JSON.stringify({
    ...op.patch, revision: op.baseRevision, operationId: op.id,
  });
}
```
Change the storage adapter and queue to persist these envelopes, not a naked `ProgressPatch`. Freeze the in-flight envelope until its result is known; later user edits become a separate operation. On a 409, reconcile the contents and persist a replacement operation before retrying. An uncertain network outcome must first retry the **same** operation ID, not generate a new ID.

For server deduplication, add a receipt table and insert the receipt in the same transaction as the progress update:
```sql
CREATE TABLE progress_operation_receipts (
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    edition_id INTEGER NOT NULL REFERENCES editions(id) ON DELETE CASCADE,
    operation_id TEXT NOT NULL,
    request_hash TEXT NOT NULL,
    applied_revision INTEGER NOT NULL,
    created_at INTEGER NOT NULL,
    PRIMARY KEY (user_id, edition_id, operation_id)
);
```
The authenticated user supplies `user_id`; never trust it from JSON. Return the original receipt for an exact retry, and reject reuse of an ID with a different request hash. Receipt lookup, conditional update, and receipt insertion must share one transaction. Bound ID/body lengths and define receipt retention together with the maximum offline-replay lifetime. This is a coordinated API/schema change, not a client-only patch.


A reusable transactional store wrapper for the receipt protocol is:
```go
var ErrOperationIDReuse = errors.New("operation id reused with different content")

func (d *DB) ApplyProgressOperation(
    userID, editionID int64, operationID, requestHash string,
    apply func(*Tx) (int64, error),
) (revision int64, replayed bool, err error) {
    if operationID == "" || len(operationID) > 64 || len(requestHash) != 64 {
        return 0, false, fmt.Errorf("invalid progress operation identity")
    }
    err = d.Update(func(tx *Tx) error {
        var previousHash string
        lookupErr := tx.QueryRow(`SELECT request_hash, applied_revision
            FROM progress_operation_receipts
            WHERE user_id = ? AND edition_id = ? AND operation_id = ?`,
            userID, editionID, operationID).Scan(&previousHash, &revision)
        if lookupErr == nil {
            if previousHash != requestHash { return ErrOperationIDReuse }
            replayed = true
            return nil
        }
        if !errors.Is(lookupErr, sql.ErrNoRows) { return lookupErr }
        var applyErr error
        revision, applyErr = apply(tx)
        if applyErr != nil { return applyErr }
        _, insertErr := tx.Exec(`INSERT INTO progress_operation_receipts
            (user_id, edition_id, operation_id, request_hash, applied_revision, created_at)
            VALUES (?,?,?,?,?,?)`, userID, editionID, operationID,
            requestHash, revision, nowMilli())
        return insertErr
    })
    return revision, replayed, err
}
```
Compute `requestHash` server-side from a canonical representation of the validated request. The `apply` callback must execute F05's conditional write through **this transaction**, returning an error on a stale base; calling an existing `d.*` writer inside the callback would escape the transaction and would not provide atomicity. Refactor the SQL into a `dbtx` helper shared by DB/Tx callers. Receipt replay does not mean the returned historical revision is the latest server head: return current state separately or let the next operation reconcile against it.

**Regression test.** Persist page 20/base 3, advance the server to page 90/revision 4, reload with revision 4, and assert that the first replay still carries base 3. Also simulate a server commit followed by a dropped response and assert that retry does not apply `finished` twice after another user's same-account device has reopened the item.

---

<a id="f03"></a>

### F03 — Conflict reconciliation forgets the winning position before the next save

**Severity: Medium.** Source-confirmed state-machine defect; isolated model reproduced.

**Source:** [`web/src/reader/shared.tsx`](https://github.com/libteca/libteca/blob/470a8edcc7e406088f71ac6e91ad99480d3f1aef/web/src/reader/shared.tsx); [`web/src/progressQueue.ts`](https://github.com/libteca/libteca/blob/470a8edcc7e406088f71ac6e91ad99480d3f1aef/web/src/progressQueue.ts); [`web/src/reader/cbz.tsx`](https://github.com/libteca/libteca/blob/470a8edcc7e406088f71ac6e91ad99480d3f1aef/web/src/reader/cbz.tsx).

**Evidence and impact.** On 409, the sender updates `revisionRef.current`. When `mergeServerProgress` returns an empty patch, it reports success without moving the reader or retaining the winning server position. A reader still displaying page 25 can therefore discard its first conflict against server page 90, then send page 26 with the now-current revision and overwrite page 90. A one-time max-merge does not make subsequent saves monotonic.

**Suggested fix.** Reconcile the visible reader state, not just the request. Alternatively retain a remote high-water mark and apply it to every automatic save until the reader catches up. Deliberate backward seeks/restarts must be explicit user intent; indiscriminately applying `max()` to all interactions would introduce a different bug.

**Implementation — reusable automatic-save reconciliation.**
```ts
import { mergeServerProgress, type ProgressPatch,
         type ServerProgress } from "./progressQueue";

type SaveIntent = "automatic" | "explicit-seek" | "explicit-restart";

export class ProgressReconciler {
  private remote: ServerProgress = {};

  observe(server: ServerProgress): void {
    this.remote = { ...server };
  }

  prepare(patch: ProgressPatch, intent: SaveIntent): ProgressPatch {
    if (intent !== "automatic") return { ...patch };
    return mergeServerProgress(this.remote, patch);
  }
}
```
Install one reconciler inside the immutable queue instance. Call `observe(current)` on a conflict and call `prepare` for **every** later automatic operation, not only for the conflicted batch. Apply the locator-only correction in F04 as well. Prefer an `onRemoteProgress` callback that lets CBZ/EPUB navigate to the accepted remote state or asks the user whether to keep the local position. An explicit reset should be a distinct revision-checked operation.

**Regression test.** Server page 90/revision 7; local automatic page 25 conflicts; local automatic page 26 follows. The stored position must remain 90 unless the user explicitly elects to move back. The isolated current-code model produced `{revision:8,page:26}`.

---

<a id="f04"></a>

### F04 — Locator-only conflicts can create inconsistent numeric and textual positions

**Severity: Medium.** Source-confirmed merge defect; isolated reproduction.

**Source:** [`web/src/progressQueue.ts`](https://github.com/libteca/libteca/blob/470a8edcc7e406088f71ac6e91ad99480d3f1aef/web/src/progressQueue.ts); [`web/src/reader/epub.tsx`](https://github.com/libteca/libteca/blob/470a8edcc7e406088f71ac6e91ad99480d3f1aef/web/src/reader/epub.tsx).

**Evidence and impact.** `mergeServerProgress` treats a patch without `page` or `percent` as locally winning and preserves its locator. EPUB emits just such patches before its location index is ready. An old locator can consequently survive a conflict against a much later server percentage. The resulting row can say “90%” but point to an earlier CFI, which EPUB then prefers when resuming.

**Suggested fix.** Treat the position tuple as a unit. A locator with no comparable numeric position must not automatically defeat a conflict. Preserve independent explicit completion intent. Once EPUB indexing becomes available, send the current locator and computed percentage together; do not silently infer CFI order by comparing strings.

**Implementation — guard at the start of the existing merge function.**
```ts
export function reconcileUnorderedLocator(
  patch: ProgressPatch,
): ProgressPatch | undefined {
  if (patch.locator === undefined || patch.page !== undefined ||
      patch.percent !== undefined) return undefined;
  return patch.finished === undefined ? {} : { finished: patch.finished };
}

// First lines inside mergeServerProgress(server, patch):
const unordered = reconcileUnorderedLocator(patch);
if (unordered !== undefined) return unordered;
```
This conservative conflict rule intentionally declines to guess which locator is newer. In the EPUB location-generation completion block, replace the current display-only percentage update with a coherent save when the location was caused by a user navigation:
```ts
if (cur?.start?.cfi) {
  const v = book.locations.percentageFromCfi(cur.start.cfi);
  if (typeof v === "number" && Number.isFinite(v)) {
    setPercent(v);
    // Gate this on actual user navigation, not initial restoration.
    saver.save({ locator: cur.start.cfi, percent: Math.max(0, Math.min(1, v)) });
  }
}
```
Add a navigation/initialization flag so a fallback display during restoration does not overwrite saved progress.

**Regression test.** Server `{percent:0.9,locator:"far"}` versus `{locator:"early"}` must not result in the server retaining `0.9` but accepting `"early"`. The current merge returned `{"locator":"early"}` in the isolated check.

---

<a id="f05"></a>

### F05 — Conditional progress writes are not safe across deletion and recreation

**Severity: High.** Source-confirmed SQL defect; isolated SQLite reproduction.

**Source:** [`internal/store/reading.go`](https://github.com/libteca/libteca/blob/470a8edcc7e406088f71ac6e91ad99480d3f1aef/internal/store/reading.go); [`internal/store/progress.go`](https://github.com/libteca/libteca/blob/470a8edcc7e406088f71ac6e91ad99480d3f1aef/internal/store/progress.go); [`internal/api/abs/abs.go`](https://github.com/libteca/libteca/blob/470a8edcc7e406088f71ac6e91ad99480d3f1aef/internal/api/abs/abs.go).

**Evidence and impact.** `SetReadingProgressRevision` places its condition only on `ON CONFLICT ... DO UPDATE`. An absent row therefore accepts **any** supplied base revision and is inserted at revision 1. `DeleteProgress` physically deletes the row, and a later creation restarts at 1. A stale request with base 1 can match an unrelated recreated row: the ABA problem. The ABS API exposes deletion of progress, so deletion is not merely hypothetical internal maintenance. This can resurrect reset progress or overwrite the new reading session.

**Suggested fix.** Separate conditional creation from conditional update and retain a deletion generation. The least disruptive conceptual approach is a tombstone in `progress`: clear user-visible progress on delete but retain/bump its revision. All writers must clear the tombstone on a legitimate new write. List/discovery queries must exclude tombstones; the single-row progress endpoint must still return their revision so a fresh client can start again.

**Implementation — coordinated migration and store changes.**
```sql
-- New migration; choose the next available migration number when applying.
ALTER TABLE progress ADD COLUMN deleted INTEGER NOT NULL DEFAULT 0
    CHECK (deleted IN (0, 1));
```
```go
// Replace physical DeleteProgress with a logical reset. A missing row is
// already reset; no row is created just to record a nonexistent history.
func (d *DB) DeleteProgress(userID, editionID int64) error {
    _, err := d.Exec(`UPDATE progress SET
        file_id = NULL, file_offset_secs = 0, edition_position_secs = 0,
        duration_secs = NULL, is_finished = 0, device = NULL,
        page = NULL, percent = NULL, locator = NULL,
        deleted = 1, revision = revision + 1, updated_at = ?
        WHERE user_id = ? AND edition_id = ?`,
        nowMilli(), userID, editionID)
    return err
}
```
For conditional creation (`baseRevision == 0`), retain the existing insert columns/arguments but use `ON CONFLICT(user_id, edition_id) DO NOTHING RETURNING revision`. For a positive base, use a conditional **UPDATE**, never the unconditional insert branch:
```go
func (d *DB) updateReadingProgressAtRevision(
    p *ReadingProgress, f ProgressFields, base int64,
) (int64, bool, error) {
    var rev int64
    err := d.QueryRow(`UPDATE progress SET
        file_id = CASE WHEN ? THEN ? ELSE file_id END,
        file_offset_secs = CASE WHEN ? THEN ? ELSE file_offset_secs END,
        edition_position_secs = CASE WHEN ? THEN ? ELSE edition_position_secs END,
        duration_secs = CASE WHEN ? THEN ? ELSE duration_secs END,
        is_finished = CASE WHEN ? THEN ? ELSE is_finished END,
        device = CASE WHEN ? THEN ? ELSE device END,
        page = coalesce(?, page), percent = coalesce(?, percent),
        locator = coalesce(?, locator), deleted = 0,
        revision = revision + 1, updated_at = ?
        WHERE user_id = ? AND edition_id = ? AND revision = ?
        RETURNING revision`,
        f.Position, p.FileID, f.Position, p.FileOffsetSecs,
        f.Position, p.EditionPositionSecs, f.Duration, p.DurationSecs,
        f.Finished, p.IsFinished, f.Device, p.Device,
        p.Page, p.Percent, p.Locator, nowMilli(),
        p.UserID, p.EditionID, base).Scan(&rev)
    if errors.Is(err, sql.ErrNoRows) { return 0, false, nil }
    return rev, err == nil, err
}
```
Add `deleted = 0` to the update clauses in `SetProgress`, `SetReadingProgressFields`, and `CloseSessionWithProgress`. Audit **every** progress read before deploying the migration; a tombstone must not appear in continue-reading, statistics, or “in progress” lists. Do not garbage-collect tombstones without a persistent generation/epoch replacement. These SQL changes need integration with the existing goose migration framework and F02 receipts.

**Regression test.** Reject base 99 on an absent row; reject stale pre-delete writes; create → advance → delete → recreate → deliver old base 1; verify rejection. The isolated SQLite run accepted base 99 on absence and accepted stale base 1 after recreation.

---

<a id="f06"></a>

### F06 — Unload beacons bypass the revision protocol and the serial queue

**Severity: Medium.** Source-confirmed behavior; documented tradeoff with data-loss risk.

**Source:** [`web/src/reader/shared.tsx`](https://github.com/libteca/libteca/blob/470a8edcc7e406088f71ac6e91ad99480d3f1aef/web/src/reader/shared.tsx); [`web/src/progressQueue.ts`](https://github.com/libteca/libteca/blob/470a8edcc7e406088f71ac6e91ad99480d3f1aef/web/src/progressQueue.ts); [`internal/api/core/core.go`](https://github.com/libteca/libteca/blob/470a8edcc7e406088f71ac6e91ad99480d3f1aef/internal/api/core/core.go).

**Evidence and impact.** `flush()` reads the pending snapshot and sends it through `postBeacon` without a revision. The same path runs on visibility changes, pagehide, beforeunload, and effect cleanup. It neither serializes with the in-flight sender nor acknowledges/removes the pending operation. A stale unconditional beacon can arrive after a newer conditional write and undo it. The code explicitly chooses this behavior; it is not an overlooked absence of a comment.

**Suggested fix.** Unload transport must use the same immutable operation envelope and base revision as normal delivery. Not being able to inspect a beacon response does **not** require an unconditional server write. Persist until an acknowledged retry, and make retries idempotent. Rejecting an uncertain stale unload write is safer than overwriting a confirmed newer position.

**Implementation — transport using the F02 envelope.**
```ts
export function sendProgressOnHide(
  op: ProgressOperation, authenticatedURL: string,
): void {
  const body = progressRequestBody(op);
  const blob = new Blob([body], { type: "application/json" });
  let queued = false;
  try {
    queued = typeof navigator.sendBeacon === "function" &&
             navigator.sendBeacon(authenticatedURL, blob);
  } catch { queued = false; }
  if (!queued) {
    void fetch(authenticatedURL, {
      method: "POST", body, keepalive: true,
      headers: { "Content-Type": "application/json" },
    }).catch(() => {});
  }
  // Keep op in durable storage. Transport acceptance is not a server ack.
}
```
Use a scope-limited progress ticket for the URL (F29), not the general account token. Coalesce lifecycle notifications by operation ID. Avoid sending a newer operation against a guessed post-in-flight revision; persist it for later reconciliation. Remove the blanket assumption in the current comment that beacon writes must be unconditional.

**Regression test.** Hold a beacon, commit newer progress through another client, release the beacon, and assert no regression. Fire visibilitychange and pagehide for the same operation and verify at-most-once application through receipts.

---

<a id="f07"></a>

### F07 — Reader queue teardown leaves live senders and shares revision state across editions

**Severity: Medium.** Source-confirmed lifecycle defect.

**Source:** [`web/src/reader/shared.tsx`](https://github.com/libteca/libteca/blob/470a8edcc7e406088f71ac6e91ad99480d3f1aef/web/src/reader/shared.tsx); [`web/src/progressQueue.ts`](https://github.com/libteca/libteca/blob/470a8edcc7e406088f71ac6e91ad99480d3f1aef/web/src/progressQueue.ts); [`web/src/api.ts`](https://github.com/libteca/libteca/blob/470a8edcc7e406088f71ac6e91ad99480d3f1aef/web/src/api.ts).

**Evidence and impact.** Cleanup calls `flush()` but not `queue.stop()`. Changing editions calls `stop()` during render, but stopping does not abort a request. Both old and new sender closures reference the same `revisionRef`, so a late old-edition response can replace the revision used by the new edition. Retry timers/late callbacks can also outlive the reader and use changed authentication. Revision conflicts often limit server corruption, but the lifetime is still incorrect and can produce unnecessary conflicts, stale UI updates, or post-logout delivery.

**Suggested fix.** Give each `(user, edition)` queue its own private revision, abort controller, and lifecycle. Stop and abort on unmount or scope change. Do not stop existing objects as a render-time side effect. Persistent pending work should remain stored even when a component is disposed.

**Implementation — instance-owned delivery context.**
```ts
export class ReaderDeliveryContext {
  readonly controller = new AbortController();
  private active = true;
  revision: number;

  constructor(readonly scope: ReaderScope, revision: number) {
    this.revision = revision;
  }
  assertActive(): void {
    if (!this.active) throw new DOMException("Reader disposed", "AbortError");
  }
  dispose(): void {
    this.active = false;
    this.controller.abort();
  }
}

// In the effect that owns a queue (not in render):
// const context = new ReaderDeliveryContext(scope, initialRevision);
// const queue = new ProgressQueue({ send: patch => sendUsing(context, patch) });
// return () => { queue.stop(); context.dispose(); };
```
Refactor `sendUsing` to use `context.revision` and `context.controller.signal`; check `context.assertActive()` both before sending and after awaiting a result, before touching state. Its revision must never be a ref shared with a replacement queue. Treat teardown abort as paused persistence rather than a new retry. Clear status-reset timers on disposal as well.

**Regression test.** Start an edition-A request, switch to B, then resolve A. B's revision and save indicator must not change. Unmount after a failed request and assert no retry timer sends later; verify the durable operation is still present.

---

<a id="f08"></a>

### F08 — Two tabs can erase each other’s durable pending progress

**Severity: Medium.** Source-confirmed shared-storage race; isolated schedule reproduced.

**Source:** [`web/src/reader/shared.tsx`](https://github.com/libteca/libteca/blob/470a8edcc7e406088f71ac6e91ad99480d3f1aef/web/src/reader/shared.tsx); [`web/src/progressQueue.ts`](https://github.com/libteca/libteca/blob/470a8edcc7e406088f71ac6e91ad99480d3f1aef/web/src/progressQueue.ts).

**Evidence and impact.** All queues for an edition share one storage entry. Each successful delivery persists its own local state and removes that entry when empty. Tab B can persist an unsent newer patch, after which tab A acknowledges an unrelated request and removes B's entry. The in-memory queues are serial individually, not across tabs. The server's revision counter does not restore a local operation erased before it was sent.

**Suggested fix.** Use per-operation storage records and acknowledge only the exact operation delivered. A single shared mutable “pending patch” key is unsuitable for independent tabs. Coordinate operation dispatch across tabs with Web Locks or a shared worker where supported, and retain CAS/idempotency as server-side protection regardless.

**Implementation — storage primitives using F01/F02.**
```ts
export function putProgressOperation(op: ProgressOperation): void {
  const key = progressPrefix(op) + op.id;
  localStorage.setItem(key, JSON.stringify(op));
}

export function acknowledgeProgressOperation(op: ProgressOperation): void {
  localStorage.removeItem(progressPrefix(op) + op.id);
}

export function loadProgressOperations(scope: ReaderScope): unknown[] {
  const prefix = progressPrefix(scope);
  const result: unknown[] = [];
  for (let i = 0; i < localStorage.length; i++) {
    const key = localStorage.key(i);
    if (!key?.startsWith(prefix)) continue;
    const raw = localStorage.getItem(key);
    if (!raw) continue;
    try { result.push(JSON.parse(raw)); }
    catch { /* quarantine/report this exact record; do not clear other keys */ }
  }
  return result;
}
```
Validate envelopes and patches (F09) before replay. Storage can throw; report degraded persistence without losing in-memory progress. For substantial outboxes, implement the same per-operation model in IndexedDB, with transactional claims/acknowledgements. Do not blindly merge operations from independent tabs into one new baseline.

**Regression test.** B writes operation B; A acknowledges operation A; operation B must remain. Repeat with both tabs sending, one offline, and a reload between persistence and acknowledgement.

---

<a id="f09"></a>

### F09 — Malformed stored patches and permanent HTTP errors are retried indefinitely

**Severity: Medium.** Source-confirmed validation/retry-policy defect.

**Source:** [`web/src/progressQueue.ts`](https://github.com/libteca/libteca/blob/470a8edcc7e406088f71ac6e91ad99480d3f1aef/web/src/progressQueue.ts); [`web/src/reader/shared.tsx`](https://github.com/libteca/libteca/blob/470a8edcc7e406088f71ac6e91ad99480d3f1aef/web/src/reader/shared.tsx).

**Evidence and impact.** The storage loader filters property names but not types/ranges; `clean()` removes only `undefined`. Every send error is reinserted and retried with backoff, including a permanently invalid 400 payload or an edition that no longer exists. Such an operation can survive reloads, repeatedly issue doomed requests, and hold the reader in an error state.

**Suggested fix.** Strictly validate the persisted representation. Separate transient failures from permanent ones. Preserve a terminal failure in a visible/quarantined record rather than silently deleting the user's work or automatically retrying forever. Make the notification callback unable to break queue housekeeping.

**Implementation.**
```ts
export function parseProgressPatch(value: unknown): ProgressPatch {
  if (!value || typeof value !== "object" || Array.isArray(value)) {
    throw new Error("Invalid saved progress object");
  }
  const v = value as Record<string, unknown>;
  const out: ProgressPatch = {};
  if (v.page !== undefined) {
    if (!Number.isSafeInteger(v.page) || (v.page as number) < 0)
      throw new Error("Invalid saved page");
    out.page = v.page as number;
  }
  if (v.percent !== undefined) {
    if (typeof v.percent !== "number" || !Number.isFinite(v.percent) ||
        v.percent < 0 || v.percent > 1) throw new Error("Invalid saved percent");
    out.percent = v.percent;
  }
  if (v.locator !== undefined) {
    if (typeof v.locator !== "string" ||
        new TextEncoder().encode(v.locator).length > 8192)
      throw new Error("Invalid saved locator");
    out.locator = v.locator;
  }
  if (v.finished !== undefined) {
    if (typeof v.finished !== "boolean") throw new Error("Invalid saved completion");
    out.finished = v.finished;
  }
  return out;
}

export function retryableProgressError(error: unknown): boolean {
  if (error instanceof AccountChangedError) return false;
  if (error instanceof APIError) {
    return error.status === 408 || error.status === 409 ||
           error.status === 429 || error.status >= 500;
  }
  if (error instanceof DOMException && error.name === "AbortError") return false;
  return true; // transport failure; use a distinct timeout error in F10
}
```
Add an option such as `shouldRetry`/`onTerminalError` to `ProgressQueue`; call `scheduleRetry()` only when policy allows. Keep the operation persisted in the terminal branch and expose retry/discard controls. Wrap `onError` in `try/catch` so an observer exception does not suppress queue cleanup or retry scheduling. Honor `Retry-After` for 429/503 where available.

**Regression test.** Reload with malformed JSON, `page:"three"`, `percent:2`, a byte-oversized locator, and a deleted edition. Assert bounded requests, visible error handling, no cross-record deletion, and continued processing after the user resolves the terminal operation.

---

<a id="f10"></a>

### F10 — A stalled network request can wedge the single-flight progress queue

**Severity: Medium.** Source-confirmed missing deadline; failure depends on transport conditions.

**Source:** [`web/src/api.ts`](https://github.com/libteca/libteca/blob/470a8edcc7e406088f71ac6e91ad99480d3f1aef/web/src/api.ts); [`web/src/reader/shared.tsx`](https://github.com/libteca/libteca/blob/470a8edcc7e406088f71ac6e91ad99480d3f1aef/web/src/reader/shared.tsx); [`web/src/progressQueue.ts`](https://github.com/libteca/libteca/blob/470a8edcc7e406088f71ac6e91ad99480d3f1aef/web/src/progressQueue.ts).

**Evidence and impact.** The normal progress sender calls `api()`/`fetch()` without a deadline. `ProgressQueue` waits for that promise before considering later work. Its retry machinery only starts after rejection; it cannot help a request that remains pending. Pending progress accumulates until the transport eventually resolves or the reader is reopened.

**Suggested fix.** Bound progress-request duration and combine it with the queue's teardown signal. Preserve operations after ambiguous timeouts and retry through CAS plus operation receipts, rather than assuming the request did not reach the server.

**Implementation — request helper; integrate at the `fetch` call in `api()`.**
```ts
export class RequestTimeoutError extends Error {}

export async function fetchTextWithDeadline(
  input: RequestInfo | URL, init: RequestInit = {}, timeoutMs = 15000,
): Promise<{ response: Response; text: string }> {
  const controller = new AbortController();
  const parent = init.signal;
  let timedOut = false;
  const relay = () => controller.abort(parent?.reason);
  if (parent?.aborted) relay();
  else parent?.addEventListener("abort", relay, { once: true });
  const timer = setTimeout(() => {
    timedOut = true;
    controller.abort();
  }, timeoutMs);
  try {
    const response = await fetch(input, { ...init, signal: controller.signal });
    const text = await response.text();
    return { response, text };
  } catch (error) {
    if (timedOut) throw new RequestTimeoutError("Progress request timed out");
    throw error;
  } finally {
    clearTimeout(timer);
    parent?.removeEventListener("abort", relay);
  }
}
```
In `api()`, use `const {response: res, text} = await fetchTextWithDeadline(...)` instead of the separate fetch/text calls, retaining the existing status/JSON/error handling. The deadline covers both headers and response-body consumption. JSON parsing remains in `api()` after receiving the bounded small API response. Do not impose this short limit on media streaming/downloads. Treat `RequestTimeoutError` as retryable but teardown abort as paused. Add a small response-size cap if the API transport must also tolerate oversized error pages; a timeout alone is not a memory budget.

**Regression test.** Mock a fetch that never resolves and one that delivers headers then stalls its body. Both must leave the operation durable, exit the in-flight state by the configured deadline, and avoid overlapping retries.

---

<a id="f11"></a>

### F11 — The PDF reader bypasses durable, revision-checked progress delivery

**Severity: Medium.** Source-confirmed incomplete rollout.

**Source:** [`web/src/reader/pdf.tsx`](https://github.com/libteca/libteca/blob/470a8edcc7e406088f71ac6e91ad99480d3f1aef/web/src/reader/pdf.tsx); [`web/src/reader/shared.tsx`](https://github.com/libteca/libteca/blob/470a8edcc7e406088f71ac6e91ad99480d3f1aef/web/src/reader/shared.tsx).

**Evidence and impact.** `PdfReader.postPage` performs fire-and-forget `apiChecked` calls and discards every rejection. It does not use `useProgressSaver`, persistence, retries, or revision checks. Its cleanup makes another ordinary fetch rather than using the shared lifecycle mechanism. The reliability improvements used by CBZ/EPUB therefore do not protect manually entered PDF progress. Rapid updates can race, and offline saves can simply disappear.

**Suggested fix.** Route PDF page/completion changes through the repaired shared saver. Load the initial revision before accepting saves, preserve errors in the UI, and clamp manually entered pages to a trustworthy known page count. The embedded native PDF viewer still does not expose actual page navigation to this component; do not imply that this change automatically tracks scrolling inside the browser's PDF viewer.

**Implementation — integrate into a loaded PDF-reader child.**
```ts
// Render this child only after its initial ReadingProgress request resolves.
// Pass userId from authenticated app state to the repaired saver (F01/F07).
const saver = useProgressSaver(props.editionId, props.progress.revision ?? 0);

const postPage = (n: number) => {
  saver.save(bodyFor(n));
};

const boundedPage = (raw: string): number | null => {
  const n = Number(raw.trim());
  if (raw.trim() === "" || !Number.isSafeInteger(n)) return null;
  const upper = countRef.current;
  return Math.max(1, upper && upper > 0 ? Math.min(n, upper) : n);
};

const markFinished = () => {
  saver.save({ finished: true });
  // Render saving/error/saved from saver.state. Do not claim server success
  // merely because enqueue() accepted the operation.
};
```
The snippet uses the current two-argument saver interface for orientation; the final component must use the authenticated-scope interface from F01. Remove the separate unmount `postPage` path and the empty catch. Let one saver own persistence and unload behavior. Expose completion as optimistic/pending until acknowledgement, or extend the queue with an acknowledgement promise.

**Regression test.** Enter two PDF pages rapidly, fail the first request, reload offline, then reconnect. Verify ordered delivery, surviving persistence, account isolation, and a visible save error. Verify that input above a reliable page count is bounded and does not store an impossible bookmark.

---

<a id="f12"></a>

### F12 — Explicit zero duration and empty device values cannot clear existing progress fields

**Severity: Medium.** Source-confirmed PATCH-semantics defect.

**Source:** [`internal/api/core/core.go`](https://github.com/libteca/libteca/blob/470a8edcc7e406088f71ac6e91ad99480d3f1aef/internal/api/core/core.go); [`internal/store/reading.go`](https://github.com/libteca/libteca/blob/470a8edcc7e406088f71ac6e91ad99480d3f1aef/internal/store/reading.go).

**Evidence and impact.** The core request decoder uses pointers to distinguish absence, but `setProgress` sets `fields.Duration` only when the supplied duration is greater than zero and sets `fields.Device` only for a nonempty string. A request that explicitly supplies `duration:0` or `device:""` is therefore treated as if the field were omitted. Existing values remain unchanged. This contradicts the purpose of the field-presence flags and makes it impossible to clear these values through this endpoint.

**Suggested fix.** Decide presence independently from value validity. Preserve omission; apply explicit valid zero/empty values. Document separately whether JSON `null` means clear or omission, since decoding into pointers currently collapses those two cases.

**Implementation — replace the two assignments in `setProgress`.**
```go
if body.Duration != nil {
    p.DurationSecs = body.Duration
    fields.Duration = true
}
if body.Device != nil {
    p.Device = body.Device
    fields.Device = true
}
```
Retain nonnegative/finite checks and byte-size limits. If the intended representation for zero duration is SQL NULL rather than zero, still set `fields.Duration = true` and explicitly set `p.DurationSecs = nil`; choose and test one contract. Implement a dedicated nullable-field decoder only if the API is to distinguish an explicit JSON null.

**Regression test.** Seed nonzero duration and a nonempty device. POST only `{duration:0,device:""}` and verify both clear while position/completion remain unchanged. POST an empty object and verify existing values remain.

---

<a id="f13"></a>

### F13 — The full-state reading-progress convenience method no longer performs a full update

**Severity: Medium.** Source-confirmed store-contract inconsistency.

**Source:** [`internal/store/reading.go`](https://github.com/libteca/libteca/blob/470a8edcc7e406088f71ac6e91ad99480d3f1aef/internal/store/reading.go); [`internal/store/progress.go`](https://github.com/libteca/libteca/blob/470a8edcc7e406088f71ac6e91ad99480d3f1aef/internal/store/progress.go).

**Evidence and impact.** `SetReadingProgress` is described as `SetProgress` plus reading fields, but calls `SetReadingProgressPatch(p,true)`, which builds `ProgressFields{Finished:true}` only. On conflict, position, file ID/offset, duration, and device are consequently preserved rather than updated. Inserts work because their values are inserted directly, making this especially easy to miss in tests that only exercise first creation. This finding concerns the method's implementation/contract; the observed core HTTP handler uses the explicit field-mask method instead.

**Suggested fix.** Make the full-state helper actually full-state, or rename/remove it and migrate every caller to explicit field flags. Keep the patch helper's semantics distinct.

**Implementation.**
```go
func (d *DB) SetReadingProgress(p *ReadingProgress) error {
    return d.SetReadingProgressFields(p, ProgressFields{
        Position: true,
        Duration: true,
        Device:   true,
        Finished: true,
    })
}
```
Check all callers before changing this exported store method: callers that intended only a reading-page update should invoke `SetReadingProgressFields` with the appropriate mask, not rely on zero-valued full-state fields. Include the tombstone changes from F05 in the underlying writer.

**Regression test.** Call `SetReadingProgress` twice with different positions, file offsets, duration, and device; verify the second call changes them. In a separate test, call the explicit patch method and verify omitted audio fields are preserved.

---

<a id="f14"></a>

### F14 — Core progress writes omit the existing position policy and accept impossible positions

**Severity: Medium.** Source-confirmed validation gap; position tolerance is a documented policy area.

**Source:** [`internal/api/core/core.go`](https://github.com/libteca/libteca/blob/470a8edcc7e406088f71ac6e91ad99480d3f1aef/internal/api/core/core.go); [`internal/store/progress.go`](https://github.com/libteca/libteca/blob/470a8edcc7e406088f71ac6e91ad99480d3f1aef/internal/store/progress.go).

**Evidence and impact.** `store.ValidPosition` implements duration-relative and unknown-duration bounds, but the inspected core `setProgress` path checks only finite/nonnegative values before calling `Locate` and persisting the original position. Extremely large finite numbers can therefore enter progress state. The request also accepts arbitrary nonnegative pages without checking a reliable page count. The historical audit register discusses position tolerance as a product decision; this report does not pretend that a chosen tolerance is a newly discovered standard requirement.

**Suggested fix.** Adopt one explicit server-side policy across faces. Use the edition's known duration, not a client-supplied duration, when validating a position. Use page-count bounds only when the count is authoritative; approximate PDF marker counts are not sufficient grounds to reject otherwise valid bookmarks.

**Implementation — before `ed.Locate`.**
```go
if body.Position != nil {
    if err := store.ValidPosition(*body.Position, ed.TotalDuration()); err != nil {
        writeJSON(w, http.StatusBadRequest,
            map[string]string{"error": err.Error()})
        return
    }
}
```
For pages, ensure values are representable by browser clients and optionally enforce a trustworthy count:
```go
const maxBrowserInteger int64 = 1<<53 - 1
if body.Page != nil && *body.Page > maxBrowserInteger {
    writeJSON(w, 400, map[string]string{"error": "page exceeds supported range"})
    return
}
```
Apply equivalent checks in relevant ABS/Subsonic/Jellyfin progress paths after reviewing their protocol semantics. Keep the existing five-second end tolerance or deliberately replace it with a documented value; do not silently choose different bounds in every face.

**Regression test.** Known duration, end-tolerance boundary, unknown duration, huge finite input, zero, and a client claiming an artificially enlarged duration. Test full-state compatibility faces separately from core patch semantics.

---

<a id="f15"></a>

### F15 — Database failures are still disguised as not-found or a fabricated conflict baseline

**Severity: Medium.** Source-confirmed error-classification defect.

**Source:** [`internal/api/core/core.go`](https://github.com/libteca/libteca/blob/470a8edcc7e406088f71ac6e91ad99480d3f1aef/internal/api/core/core.go); [`internal/api/core/hls.go`](https://github.com/libteca/libteca/blob/470a8edcc7e406088f71ac6e91ad99480d3f1aef/internal/api/core/hls.go).

**Evidence and impact.** The inspected `work`, `setProgress` edition lookup, scan-job lookup paths, and HLS source lookups return 404 for arbitrary store failures. More importantly, after a rejected progress CAS, **any** `GetReadingProgress` error is converted into a synthetic revision-zero row and a 409 response. A database failure is not proof that progress is absent. The synthetic conflict state can mislead the client into an invalid rebase and hide an operational incident.

**Suggested fix.** Distinguish `store.ErrNotFound` from infrastructure/query errors. Return sanitized 500/503 for unexpected failures, log the underlying cause, and do not invent current progress. With F05 tombstones, an ordinary reset should have a real revision to return.

**Implementation — replace the conflict reread fallback.**
```go
cur, err := a.DB.GetReadingProgress(auth.UserID(r), eid)
if err != nil {
    if errors.Is(err, store.ErrNotFound) {
        cur = &store.ReadingProgress{Progress: store.Progress{
            UserID: auth.UserID(r), EditionID: eid, Revision: 0,
        }}
    } else {
        slog.Error("progress conflict reread failed", "edition", eid, "err", err)
        writeJSON(w, http.StatusServiceUnavailable,
            map[string]string{"error": "progress temporarily unavailable"})
        return
    }
}
```
Use the same pattern for other lookups: 404 only for `ErrNotFound`; otherwise 500/503. If an endpoint intentionally hides the difference for authorization reasons, document that separately rather than applying the rule to database outages.

**Regression test.** Inject a non-not-found database error during the edition read and during the post-CAS reread. Assert neither returns a 404 nor a fabricated `{revision:0}` 409. Verify genuine missing rows retain the intended contract.

---

<a id="f16"></a>

### F16 — Several progress readers leave the newly introduced revision field at zero

**Severity: Low.** Source-confirmed read-model inconsistency.

**Source:** [`internal/store/progress.go`](https://github.com/libteca/libteca/blob/470a8edcc7e406088f71ac6e91ad99480d3f1aef/internal/store/progress.go); [`internal/store/reading.go`](https://github.com/libteca/libteca/blob/470a8edcc7e406088f71ac6e91ad99480d3f1aef/internal/store/reading.go).

**Evidence and impact.** `GetReadingProgress` selects/scans `revision`, but `GetProgress`, `UserProgressList`, and `ReadingListByUser` do not. Their embedded `Progress.Revision` therefore remains zero even when a row has advanced. This is not evidence that every current compatibility client consumes revisions; it is an incomplete store read contract that creates incorrect data for future conditional writers and any caller assuming the field is populated.

**Suggested fix.** Include revision in all progress projections, or define separate types that explicitly do not expose it. Apply tombstone filtering consistently when implementing F05.

**Implementation — repeat the added column/scan destination in the three readers.**
```go
err := d.QueryRow(`SELECT user_id, edition_id, file_id, file_offset_secs,
    edition_position_secs, duration_secs, is_finished, device, updated_at,
    revision FROM progress WHERE user_id = ? AND edition_id = ?`,
    userID, editionID).Scan(
    &p.UserID, &p.EditionID, &p.FileID, &p.FileOffsetSecs,
    &p.EditionPositionSecs, &p.DurationSecs, &fin, &p.Device, &p.UpdatedAt,
    &p.Revision,
)
```
The loop readers need the same extra destination on each `rows.Scan`; `ReadingListByUser` must retain its page/percent/locator fields as well. Do not reorder SELECT columns without matching the scan list.

**Regression test.** Advance a row through several writers and read it using all four accessors. Each accessor that exposes `Revision` must return the same value. Keep compatibility response formats unchanged unless explicitly extending their protocol.

---

<a id="f17"></a>

### F17 — Browser archive readers have no explicit compressed/uncompressed memory budgets

**Severity: Medium.** Source-confirmed resource-hardening gap.

**Source:** [`web/src/reader/cbz.tsx`](https://github.com/libteca/libteca/blob/470a8edcc7e406088f71ac6e91ad99480d3f1aef/web/src/reader/cbz.tsx); [`web/src/reader/epub.tsx`](https://github.com/libteca/libteca/blob/470a8edcc7e406088f71ac6e91ad99480d3f1aef/web/src/reader/epub.tsx); [`web/package.json`](https://github.com/libteca/libteca/blob/470a8edcc7e406088f71ac6e91ad99480d3f1aef/web/package.json).

**Evidence and impact.** CBZ and EPUB download the complete body into an `ArrayBuffer`. CBZ then loads the archive through JSZip and extracts pages with `entry.async("blob")` without a byte ceiling. Evicting distant object URLs does not bound the initial archive, the number of entries, or the decompression of one oversized page. CBZ also lacks the EPUB reader's fetch abort controller. Large or highly compressed library content can exhaust the browser's memory or continue downloading after leaving the reader. This is a client-resource issue, not a demonstrated server remote-code-execution vulnerability or a claim that a particular dependency version is vulnerable.

**Suggested fix.** Enforce separate compressed-body, entry-count, per-page uncompressed-byte, and image-dimension budgets. Abort on teardown. Prefer server-side paginated/range-aware reading for large archives, and isolate expensive parsing in a terminable worker. Limits below are example policy values, not universal format limits.

**Implementation — bounded response reading.**
```ts
export async function readBoundedBody(
  response: Response, limit: number,
): Promise<ArrayBuffer> {
  if (!response.ok) throw new Error(`Download failed (${response.status})`);
  const length = Number(response.headers.get("Content-Length"));
  if (Number.isFinite(length) && length > limit) throw new Error("Book too large");
  if (!response.body) throw new Error("Streaming response body unavailable");
  const reader = response.body.getReader();
  const parts: Uint8Array[] = [];
  let size = 0;
  try {
    for (;;) {
      const { done, value } = await reader.read();
      if (done) break;
      if (value.byteLength > limit - size) throw new Error("Book too large");
      size += value.byteLength;
      parts.push(value);
    }
  } catch (error) {
    await reader.cancel().catch(() => {});
    throw error;
  } finally {
    reader.releaseLock();
  }
  const result = new Uint8Array(size);
  let offset = 0;
  for (const part of parts) { result.set(part, offset); offset += part.byteLength; }
  return result.buffer;
}
```
Use it instead of `res.arrayBuffer()`, with an AbortController on the original fetch. Account for the additional bounded copy when selecting the limit.

**Implementation — bounded JSZip page extraction.**
```ts
import type JSZip from "jszip";

export function readZipPage(
  entry: JSZip.JSZipObject, limit: number, signal: AbortSignal,
): Promise<Blob> {
  return new Promise((resolve, reject) => {
    const parts: ArrayBuffer[] = [];
    let size = 0, settled = false;
    const stream = entry.internalStream("uint8array");
    const cleanup = () => signal.removeEventListener("abort", abort);
    const fail = (error: unknown) => {
      if (settled) return;
      settled = true;
      stream.pause();
      parts.length = 0;
      cleanup();
      reject(error);
    };
    const abort = () => fail(new DOMException("Reader closed", "AbortError"));
    stream.on("data", (chunk: Uint8Array) => {
      if (settled) return;
      if (chunk.byteLength > limit - size) { fail(new Error("Page too large")); return; }
      size += chunk.byteLength;
      const copy = new Uint8Array(chunk.byteLength);
      copy.set(chunk);
      parts.push(copy.buffer);
    });
    stream.on("error", fail);
    stream.on("end", () => {
      if (settled) return;
      settled = true;
      cleanup();
      resolve(new Blob(parts));
    });
    if (signal.aborted) abort();
    else { signal.addEventListener("abort", abort, { once: true }); stream.resume(); }
  });
}
```
Reject excessive archive entry counts before constructing `PageStore`. A byte cap is not a CPU or decoded-pixel cap; add worker termination and dimension checks rather than calling this a complete hostile-parser sandbox. EPUB's library-managed internal extraction needs its own budget strategy or a server-side conversion path.

**Regression test.** Oversized Content-Length, unknown-length streamed overflow, one compressed page expanding past the cap, excessive entries, very large image dimensions, and navigation away during download/extraction. Verify a recoverable error, not an endlessly busy reader.

---

<a id="f18"></a>

### F18 — A failed CBZ page is rendered as permanently “loading”

**Severity: Low.** Source-confirmed UI/error-state defect.

**Source:** [`web/src/reader/cbz.tsx`](https://github.com/libteca/libteca/blob/470a8edcc7e406088f71ac6e91ad99480d3f1aef/web/src/reader/cbz.tsx).

**Evidence and impact.** `PageStore.drain` stores `null` after extraction failure. `ready()` treats that slot as completed, `ensure()` will not retry it, and `PageImg` renders its generic loading placeholder when the URL is null. A corrupt/unsupported page therefore looks like an endless download with no explanation or retry path.

**Suggested fix.** Distinguish not-requested/loading, failed, and ready states. Expose a retry method that clears only the failed slot, and show an error action instead of the loading placeholder.

**Implementation — add to `PageStore`.**
```ts
status(i: number): "loading" | "error" | "ready" {
  return this.urls[i] === null ? "error" :
         typeof this.urls[i] === "string" ? "ready" : "loading";
}

retry(i: number): void {
  if (this.revoked || i < 0 || i >= this.count) return;
  if (this.urls[i] !== null) return;
  delete this.urls[i];
  this.ensure(i, true);
  this.notify();
}
```
Extend `PageImg` with `failed` and `onRetry` props and render a labeled retry button when failed:
```tsx
if (props.failed) {
  return <div role="alert">
    This page could not be decoded.
    <button onClick={props.onRetry}>Retry page</button>
  </div>;
}
```
Ensure the retry uses the extraction limits from F17 and does not loop automatically on a consistently corrupt entry.

**Regression test.** Make page extraction reject once, verify an error replaces “loading,” then retry successfully. A permanently invalid image should remain a clear error, not consume a retry timer indefinitely.

---

<a id="f19"></a>

### F19 — EPUB location caches are keyed by edition ID rather than content identity

**Severity: Medium.** Source-confirmed cache-invalidation gap.

**Source:** [`web/src/reader/epub.tsx`](https://github.com/libteca/libteca/blob/470a8edcc7e406088f71ac6e91ad99480d3f1aef/web/src/reader/epub.tsx).

**Evidence and impact.** Cached generated locations use `libteca-epub-loc-${editionId}`. Replacing/reimporting an EPUB while keeping the edition ID can leave a cached CFI index for different bytes. `haveLocations` checks only that the loaded index is nonempty. Resume and percentage calculations can then use the wrong index. Falling back from a bad target to the beginning is not sufficient if relocation handlers subsequently save that fallback as new progress.

**Suggested fix.** Key the generated-location cache by the content fingerprint and index-generation version. Distinguish failed restoration from user navigation, and do not persist a fallback position until the user actually moves or accepts the reset.

**Implementation — the reader already has the full downloaded buffer.**
```ts
export async function epubLocationKey(
  editionId: number, data: ArrayBuffer, chunkSize: number,
): Promise<string> {
  const digest = await crypto.subtle.digest("SHA-256", data);
  const hex = Array.from(new Uint8Array(digest),
    b => b.toString(16).padStart(2, "0")).join("");
  return `libteca:epub-locations:v2:${editionId}:${chunkSize}:${hex}`;
}

// After bounded download, before cache load/generation:
const locKey = await epubLocationKey(props.editionId, data, LOC_CHUNK);
```
Use this exact key for both `locations.load` and `persistLocations`. If Web Crypto is unavailable in the deployment context, skip the cache or use a trusted server-provided content version rather than falling back to an unsafe ID-only key. A hash is a cache identity here, not a claim about authenticating book content. Bound/prune old cache entries.

**Regression test.** Open book bytes A, cache the index, replace them with bytes B under the same edition ID, reopen, and assert index regeneration. Test malformed cached data and an invalid saved CFI without overwriting the previous server bookmark during initialization.

---

<a id="f20"></a>

### F20 — Metadata SSE subscription can send on a closed channel and lose the terminal event

**Severity: Medium.** Source-confirmed concurrency defect.

**Source:** [`internal/api/core/providers.go`](https://github.com/libteca/libteca/blob/470a8edcc7e406088f71ac6e91ad99480d3f1aef/internal/api/core/providers.go).

**Evidence and impact.** `metaRun.subscribe` registers a new channel under `r.mu`, unlocks, and only then sends the initial snapshot. `finish` can run between those steps and close the channel; the subsequent initial send panics. In normal net/http handling this can abort the request; this report does not claim that every occurrence kills the entire process. There is a second lifecycle issue: `finish` drops the terminal snapshot when the channel is full, then closes it, so a subscriber may consume only “running” snapshots before EOF.

**Suggested fix.** Publish the initial buffered snapshot while holding the same lock that protects registration/closure. Publish and close terminal events under that lock, making room by dropping an obsolete running event rather than the final state.

**Implementation — replacements for the two methods.**
```go
func (r *metaRun) subscribe() chan metaSnap {
    ch := make(chan metaSnap, 8)
    r.mu.Lock()
    defer r.mu.Unlock()
    ch <- r.snap // Fresh buffered channel: cannot block.
    if r.closed {
        close(ch)
        return ch
    }
    if r.subs == nil { r.subs = map[chan metaSnap]struct{}{} }
    r.subs[ch] = struct{}{}
    return ch
}

func (r *metaRun) finish(s metaSnap) {
    r.mu.Lock()
    defer r.mu.Unlock()
    if r.closed { return }
    r.snap = s
    r.closed = true
    for ch := range r.subs {
        select {
        case ch <- s:
        default:
            // No publisher can fill it while this lock is held.
            select { case <-ch: default: }
            ch <- s
        }
        close(ch)
    }
    r.subs = nil
}
```
Add `if r.closed { return }` to `publish` under its existing lock so a late publisher cannot change the terminal snapshot. The fixed `subscribe`, `publish`, and `finish` must all share the same locking discipline. Do not “fix” this by recovering the panic while leaving the ordering race.

**Regression test.** Repeatedly race subscribe/finish under `go test -race`, subscribe after finish, and fill a subscriber's buffer before finishing. Assert no send-on-closed panic and exactly one terminal state available before channel closure.

---

<a id="f21"></a>

### F21 — Optional metadata failures are silently converted into successful-looking results

**Severity: Medium.** Source-confirmed partial-failure handling defect.

**Source:** [`internal/api/core/providers.go`](https://github.com/libteca/libteca/blob/470a8edcc7e406088f71ac6e91ad99480d3f1aef/internal/api/core/providers.go).

**Evidence and impact.** The inspected metadata application code ignores errors from genre updates and episode application. `applyChapters` returns only a count while swallowing marshal/database failures. `distributeChapters` returns zero on query/scan failures and closes its rows without checking `rows.Err()` before performing writes. A truncated/erroring read can therefore look like an empty or partially successful metadata result. Optional metadata need not make an entire operation fail, but omission of any warning makes it impossible to distinguish “no data to apply” from “application failed.”

**Suggested fix.** Return `(count,error)` from chapter helpers, check iteration errors before any writes, and return structured warnings for optional failures. Use a transaction for a set of database writes that the UI presents as one atomic operation. Do not hold a database transaction open while contacting a remote provider.

**Implementation — checked row termination and warning aggregation.**
```go
// After the rows.Next()/Scan loop in distributeChapters, before writes:
if err := rows.Err(); err != nil {
    rows.Close()
    return 0, err // Change this helper to return (int64, error).
}
if err := rows.Close(); err != nil {
    return 0, err
}
```
```go
func recordMetadataPart(summary map[string]any, part string,
    apply func() (int64, error)) {
    n, err := apply()
    if err == nil {
        summary[part] = n
        return
    }
    slog.Warn("optional metadata application failed", "part", part, "err", err)
    warnings, _ := summary["warnings"].([]string)
    summary["warnings"] = append(warnings, part+" could not be applied")
}
```
Use `recordMetadataPart` for chapters/genres/episodes after adapting their signatures, and display warnings in the metadata-apply UI. Preserve internal details in logs, not in non-admin responses. Treat a primary work update failure as an actual failed operation, rather than downgrading everything to warnings.

**Regression test.** Inject failure during row iteration and during each auxiliary update. Assert that iteration failure causes no writes, and that a partially successful optional operation returns a visible warning instead of an indistinguishable success. Verify transaction rollback for atomic groups.

---

<a id="f22"></a>

### F22 — Chapter replacement checks stale values but updates without comparing them

**Severity: Medium.** Source-confirmed check-then-write race.

**Source:** [`internal/api/core/providers.go`](https://github.com/libteca/libteca/blob/470a8edcc7e406088f71ac6e91ad99480d3f1aef/internal/api/core/providers.go).

**Evidence and impact.** `distributeChapters` reads stored chapter JSON, calls `genericChapters`, and later executes `UPDATE files SET chapters = ? WHERE id = ? AND missing = 0`. The UPDATE does not verify that the chapters are still the generic value inspected. A concurrent scanner/provider update can write authoritative chapter titles between the read and write, and this operation can overwrite them despite the comment promising that embedded titles are never clobbered.

**Suggested fix.** Use compare-and-swap against the exact original chapter value, or perform the decision and update in a correctly isolated transaction. Check `RowsAffected`; a zero-row result is a concurrent modification, not a successful replacement.

**Implementation — store helper.**
```go
func (d *DB) ReplaceChaptersIfUnchanged(
    fileID int64, previous, replacement string,
) (bool, error) {
    result, err := d.Exec(`UPDATE files SET chapters = ?
        WHERE id = ? AND missing = 0 AND chapters IS ?`,
        replacement, fileID, previous)
    if err != nil { return false, err }
    n, err := result.RowsAffected()
    return n == 1, err
}
```
Use the returned boolean when incrementing `written`, and report or retry a conflict after rereading/rechecking `genericChapters`. If nullable chapters are supported, preserve `sql.NullString` through the read and compare its original nullable value, rather than coercing NULL into an empty string. Combine this with F21's checked reads and error propagation.

**Regression test.** Pause immediately after the generic-chapter read, write authoritative chapters from another connection, resume the provider update, and assert the authoritative value is preserved and the overwrite is not counted as a success.

---

<a id="f23"></a>

### F23 — Provider chapters spanning file boundaries lose their continuation

**Severity: Medium.** Source-confirmed multipart mapping limitation.

**Source:** [`internal/api/core/providers.go`](https://github.com/libteca/libteca/blob/470a8edcc7e406088f71ac6e91ad99480d3f1aef/internal/api/core/providers.go).

**Evidence and impact.** `distributeChapterMath` assigns each chapter only to the file containing its start, then clamps its end to that file's duration. A chapter spanning two files loses the part in the second file. For durations `[30,40]` and a chapter `[10,70]`, the function emits only file 1's `[10,30]`, leaving file 2's `[0,40]` without that chapter. This matches the current comment's algorithm but is incomplete for a per-file chapter timeline.

**Suggested fix.** Intersect each edition-wide chapter interval with every file interval. Preserve the provider chapter's identity/title for each continuation. If durations are unknown, do not guess offsets for subsequent files: return a warning and leave the original chapters unchanged.

**Implementation — replacement math helper; adapt its caller to propagate errors.**
```go
func distributeChapterMath(
    durations []float64, chapters []meta.Chapter,
) ([][]fileChapter, error) {
    finite := func(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }
    for _, d := range durations {
        if !finite(d) || d <= 0 {
            return nil, fmt.Errorf("cannot map chapters across unknown file duration")
        }
    }
    for _, c := range chapters {
        if !finite(c.StartSec) || !finite(c.EndSec) ||
            c.StartSec < 0 || c.EndSec <= c.StartSec {
            return nil, fmt.Errorf("invalid provider chapter interval")
        }
    }
    out := make([][]fileChapter, len(durations))
    base := 0.0
    for fileIndex, duration := range durations {
        endOfFile := base + duration
        if !finite(endOfFile) { return nil, fmt.Errorf("edition duration overflow") }
        for chapterIndex, c := range chapters {
            lo := math.Max(base, c.StartSec)
            hi := math.Min(endOfFile, c.EndSec)
            if hi <= lo { continue }
            out[fileIndex] = append(out[fileIndex], fileChapter{
                ID: int64(chapterIndex + 1), Title: c.Title,
                Start: lo - base, End: hi - base,
            })
        }
        base = endOfFile
    }
    return out, nil
}
```
This requires adding `math` if it is not already imported and updating existing tests/call sites for the error return. Decide whether the client labels continuations specially; do not alter chapter titles implicitly in the store unless that is an explicit product choice.

**Regression test.** Chapters wholly inside one file, exactly on a boundary, spanning two or three files, beyond the edition end, and files with unknown durations. Assert no negative intervals or uncovered continuation caused by clipping.

---

<a id="f24"></a>

### F24 — Cover validation accepts non-JPEG and header-only images but stores a .jpg filename

**Severity: Medium.** Source-confirmed representation/validation mismatch.

**Source:** [`internal/api/core/providers.go`](https://github.com/libteca/libteca/blob/470a8edcc7e406088f71ac6e91ad99480d3f1aef/internal/api/core/providers.go); [`internal/api/core/core.go`](https://github.com/libteca/libteca/blob/470a8edcc7e406088f71ac6e91ad99480d3f1aef/internal/api/core/core.go).

**Evidence and impact.** `downloadCover` accepts JPEG, PNG, or GIF after `image.DecodeConfig`, but selects `${workID}.jpg` and writes the downloaded bytes. A PNG/GIF can therefore be stored and served under a JPEG filename/content-type path. `DecodeConfig` validates enough of an image to report configuration, not necessarily a complete decodable image, so a header-valid truncated payload can also be cached. Some consumers are tolerant; that does not make the representation consistent or prove all downloaded covers decode successfully.

**Suggested fix.** After the existing byte and dimension checks, fully decode and normalize the static cover to JPEG, or preserve the actual extension/MIME throughout the database and every API/client. Normalizing is less disruptive because the existing paths assume `.jpg`. Repair invalid existing cache entries instead of returning true solely because `os.Stat(dst)` succeeds.

**Implementation — replace writing the original bytes with JPEG encoding.**
```go
// Additional imports: image/color, image/draw, image/jpeg.
img, _, err := image.Decode(bytes.NewReader(data))
if err != nil {
    return false, fmt.Errorf("cover download: incomplete or invalid image: %w", err)
}
bounds := img.Bounds()
if bounds.Dx() <= 0 || bounds.Dy() <= 0 ||
    int64(bounds.Dx())*int64(bounds.Dy()) > 16_000_000 {
    return false, fmt.Errorf("cover download: decoded dimensions exceed budget")
}
canvas := image.NewRGBA(bounds)
draw.Draw(canvas, bounds, image.NewUniform(color.White), image.Point{}, draw.Src)
draw.Draw(canvas, bounds, img, bounds.Min, draw.Over)

// `tmp` is the existing same-directory os.CreateTemp result.
if err := jpeg.Encode(tmp, canvas, &jpeg.Options{Quality: 90}); err != nil {
    tmp.Close()
    os.Remove(tmp.Name())
    return false, err
}
```
Retain checked close, cleanup, and atomic publication; add the currently missing file/directory sync steps from F37. Do not append the original downloaded bytes after the encoded JPEG. Use the existing 16-million-pixel policy or another deliberately reviewed value. For GIF, explicitly document that a cover is a static first-frame image.

**Regression test.** PNG/GIF input results in bytes decodable as JPEG at the `.jpg` path; a truncated image with a readable header is rejected; existing invalid cache content can be replaced; transparent input uses the chosen background color.

---

<a id="f25"></a>

### F25 — Generation directories do not by themselves make the database and covers one consistent snapshot

**Severity: Medium.** Source-confirmed design limitation; concurrent-writer outcome not integration-tested.

**Source:** [`internal/store/backup.go`](https://github.com/libteca/libteca/blob/470a8edcc7e406088f71ac6e91ad99480d3f1aef/internal/store/backup.go); [`internal/api/core/providers.go`](https://github.com/libteca/libteca/blob/470a8edcc7e406088f71ac6e91ad99480d3f1aef/internal/api/core/providers.go).

**Evidence and impact.** `Snapshot` first runs `VACUUM INTO`, then copies the live cover tree. Its `.backup.lock` excludes other backup operations, not cover writers or deletions. Generation isolation correctly prevents one backup from overwriting another's cover directory, but it does not establish a single snapshot boundary across the database and filesystem. A cover reference/content changed or removed between those steps can yield a generation whose database and cover bytes describe different states. This is a residual consistency concern, not a repetition of the already-fixed shared-covers-generation bug.

**Suggested fix.** Coordinate *all* cover-changing filesystem/DB transactions with snapshots, or adopt immutable content-addressed assets with snapshot-aware retention. A cooperative lock is the smaller architectural change, though copying under the lock delays metadata/cover updates. Unrelated progress writes can continue.

**Implementation — cross-process asset lock helper for the existing Unix deployment model.**
```go
func withAssetLock(ctx context.Context, lockPath string, exclusive bool,
    fn func() error) error {
    f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
    if err != nil { return err }
    defer f.Close()
    mode := syscall.LOCK_SH
    if exclusive { mode = syscall.LOCK_EX }
    tick := time.NewTicker(25 * time.Millisecond)
    defer tick.Stop()
    for {
        if err := ctx.Err(); err != nil { return err }
        err = syscall.Flock(int(f.Fd()), mode|syscall.LOCK_NB)
        if err == nil { break }
        if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EAGAIN) {
            return err
        }
        select {
        case <-ctx.Done(): return ctx.Err()
        case <-tick.C:
        }
    }
    defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
    return fn()
}
```
Add a context-aware snapshot entry point and acquire this lock **exclusively** across `BackupTo` plus cover copying. Every cover publication/deletion and its corresponding database-reference update must acquire the same lock **shared** for the complete mutation. Fetch/decode remote images *before* acquiring the mutation lock. Use one canonical lock path under `DataDir` in both server and CLI; enforce one lock acquisition order with `.backup.lock` to avoid deadlocks. A lock used only by `Snapshot` would not fix the issue.

For a more scalable design, store assets by content hash, never overwrite them in place, derive the referenced set from the database snapshot, and pin that set against garbage collection until copying completes. Merely adding filenames/checksums to a manifest does not create consistency.

**Regression test.** Pause after database snapshot creation, attempt cover replacement/deletion from another cooperating writer, and verify it blocks until the generation has copied the matching bytes. Restore the generation and verify every referenced asset exists. Also test writer and backup processes separately, not only goroutines.

---

<a id="f26"></a>

### F26 — A missing cover source can be silently accepted as a successful backup

**Severity: Medium.** Source-confirmed backup completeness defect.

**Source:** [`internal/store/backup.go`](https://github.com/libteca/libteca/blob/470a8edcc7e406088f71ac6e91ad99480d3f1aef/internal/store/backup.go).

**Evidence and impact.** In `copyTree`, a `WalkDir` error for a nonexistent path is ignored. If the cover root is missing, copying can return success, publish the database-only generation, and run retention. This may be harmless for a genuinely new installation with no covers, but it is dangerous after a missing mount, accidental directory deletion, or a concurrently disappeared subtree. The routine cannot distinguish “nothing to back up” from “expected assets unavailable.” Pruning older complete generations after such a result makes the failure more consequential.

**Suggested fix.** Fail closed on a missing configured asset root during backup, except through an explicit, validated empty-install path. Create the empty covers directory during initialization, not opportunistically after a backup source disappears. Validate the snapshot's referenced asset set before retention.

**Implementation — fail instead of suppressing walk errors.**
```go
func requireCoverRoot(path string) error {
    info, err := os.Stat(path)
    if err != nil { return fmt.Errorf("backup cover source unavailable: %w", err) }
    if !info.IsDir() { return fmt.Errorf("backup cover source is not a directory") }
    return nil
}

// At the start of Snapshot's locked operation, before creating a stage:
if err := requireCoverRoot(coversDir); err != nil { return err }

// At the start of copyTree's WalkDir callback:
if err != nil { return err } // Remove the os.IsNotExist(err) => nil branch.
```
This is a deliberate tightening: tests or callers that currently pass a nonexistent empty covers directory must initialize an actual empty directory first. A full referenced-assets check must include all cover-owning tables, not just works. Do not publish or prune on a completeness failure.

**Regression test.** Remove/rename the covers root before snapshotting and a subtree during copying. Assert an error, no published generation, and no deletion of prior valid backups. A valid empty initialized directory should still produce a successful empty-asset generation.

---

<a id="f27"></a>

### F27 — Backup copying still has a path-check-to-open confinement race

**Severity: Low.** Source-confirmed defense-in-depth gap; requires a concurrent filesystem writer.

**Source:** [`internal/store/backup.go`](https://github.com/libteca/libteca/blob/470a8edcc7e406088f71ac6e91ad99480d3f1aef/internal/store/backup.go).

**Evidence and impact.** `copyTree` rejects a nonregular `DirEntry`, then separately calls `os.Open(p)`. A file can be replaced with a symlink between those operations, and the open follows it. A special file swapped into place can also block an ordinary open before its type is checked. This is conditional on a writer capable of mutating the cover directory; it is not an established unauthenticated remote escape, and a private server-owned data directory substantially reduces the risk.

**Suggested fix.** Pin the source directory with `os.Root`, open relative to that root, and inspect the opened descriptor. Use a nonblocking open for the regular-file check on the repository's Unix targets. Keep the cooperative asset lock from F25 for ordinary writers.

**Implementation — establish once per copy, then replace the path opener.**
```go
root, openErr := os.OpenRoot(src)
if openErr != nil { return openErr }
defer root.Close()

// Inside the walk callback, after deriving a relative file path `rel`:
in, err := root.OpenFile(rel, os.O_RDONLY|syscall.O_NONBLOCK, 0)
if err != nil { return err }
info, err := in.Stat()
if err != nil {
    in.Close()
    return err
}
if !info.Mode().IsRegular() {
    in.Close()
    return fmt.Errorf("unsupported opened cover entry: %s", rel)
}
// Copy from this exact `in`; do not reopen its pathname.
```
The repository's declared Go version supports the rooted API; the local audit environment did not have that toolchain. Preserve checked input/output closes and atomic output replacement. This protects the open boundary; it does not authenticate a hostile writer's legitimate in-root content or freeze arbitrary external modifications.

**Regression test.** Swap an entry to an escaping symlink and a FIFO between enumeration and open. The copy must reject/contain the entry and must not hang or copy an outside file.

---

<a id="f28"></a>

### F28 — Crashed backup stages accumulate and retention deletions are not directory-synced

**Severity: Low.** Source-confirmed operational durability gap.

**Source:** [`internal/store/backup.go`](https://github.com/libteca/libteca/blob/470a8edcc7e406088f71ac6e91ad99480d3f1aef/internal/store/backup.go).

**Evidence and impact.** `Snapshot` removes its private stage only with `defer`, which cannot execute after process death. Retention recognizes published generations/legacy databases, not abandoned `.libteca-stage-*` directories. Repeated interrupted backups can therefore consume disk outside the retention budget. Additionally, publication syncs the backup directory, but `pruneBackups` returns after deletions without syncing the directory again; successful retention is not explicitly made durable against a crash.

**Suggested fix.** Under the existing cross-process backup lock, clean abandoned unpublished stages before creating the new stage. Sync directory metadata after retention changes. Keep cleanup restricted to the application's dedicated backup directory and reserved stage namespace.

**Implementation.**
```go
func cleanupBackupStages(dir string) error {
    entries, err := os.ReadDir(dir)
    if err != nil { return err }
    for _, entry := range entries {
        if !entry.IsDir() || !strings.HasPrefix(entry.Name(), ".libteca-stage-") {
            continue
        }
        if err := os.RemoveAll(filepath.Join(dir, entry.Name())); err != nil {
            return fmt.Errorf("remove abandoned backup stage: %w", err)
        }
    }
    return syncPath(dir)
}
```
Call this only **after** acquiring `.backup.lock` and **before** `MkdirTemp` creates the current operation's stage. All stage-producing code must use the same lock. At the successful end of `pruneBackups`, replace `return nil` with `return syncPath(dir)` after any deletions. Preserve the `keep < 1` no-pruning behavior.

**Regression test.** Kill a backup process after stage creation, start another backup, and verify abandoned stages are removed while published generations remain untouched. Inject deletion/sync failures and expose them accurately; distinguish “generation published, retention failed” from “no backup created” in caller messaging.

---

<a id="f29"></a>

### F29 — Media URLs expose a general bearer credential rather than a narrowly scoped capability

**Severity: Medium.** Source-confirmed credential-lifecycle risk; substantially documented as deferred work.

**Source:** [`web/src/api.ts`](https://github.com/libteca/libteca/blob/470a8edcc7e406088f71ac6e91ad99480d3f1aef/web/src/api.ts); [`web/src/reader/shared.tsx`](https://github.com/libteca/libteca/blob/470a8edcc7e406088f71ac6e91ad99480d3f1aef/web/src/reader/shared.tsx); [`internal/auth/auth.go`](https://github.com/libteca/libteca/blob/470a8edcc7e406088f71ac6e91ad99480d3f1aef/internal/auth/auth.go); [`internal/api/core/hls.go`](https://github.com/libteca/libteca/blob/470a8edcc7e406088f71ac6e91ad99480d3f1aef/internal/api/core/hls.go).

**Evidence and impact.** `media()` appends the account token to URLs, the auth middleware accepts that query token generally, and HLS rewriting propagates it into child URLs. These are full credentials, not resource-specific tickets. They can be retained in access logs, copied URLs, browser tooling, and other URL-handling systems. Token lookup has a revocation check but no expiry predicate in the inspected implementation. Digesting stored token values fixes at-rest exposure, not exposure of the raw credential in a URL. This is a deployment-dependent risk; no actual leaked token was obtained during the review.

**Suggested fix.** Keep ordinary account authentication in headers. Have authenticated API responses return short-lived, method/resource-scoped media URLs; bind them to a still-active parent token so revocation also invalidates derived capabilities. Restrict query authentication to those intended media routes, and give progress beacons their own narrowly scoped capability. Add a deliberate token-expiry/rotation policy and per-account token-count limits without silently breaking long-lived third-party client tokens.

**Implementation — standalone signed ticket primitive for a new Go file.**
```go
package core

import (
    "crypto/hmac"
    "crypto/rand"
    "crypto/sha256"
    "encoding/base64"
    "encoding/json"
    "errors"
    "net/http"
    "strings"
    "time"
)

type MediaTicketClaims struct {
    UserID       int64  `json:"u"`
    Method       string `json:"m"`
    Target       string `json:"p"`
    ParentDigest string `json:"t"`
    Expires      int64  `json:"e"`
}

type MediaTicketSigner struct { key [32]byte }

func NewMediaTicketSigner() (*MediaTicketSigner, error) {
    signer := new(MediaTicketSigner)
    _, err := rand.Read(signer.key[:])
    return signer, err
}

func mediaTicketTarget(r *http.Request) string {
    q := r.URL.Query()
    q.Del("media_ticket")
    target := r.URL.EscapedPath()
    if encoded := q.Encode(); encoded != "" { target += "?" + encoded }
    return target
}

func (s *MediaTicketSigner) Sign(c MediaTicketClaims) (string, error) {
    now := time.Now().Unix()
    if c.UserID <= 0 || c.ParentDigest == "" || c.Target == "" ||
        c.Expires <= now || c.Expires > now+300 {
        return "", errors.New("invalid media ticket claims")
    }
    raw, err := json.Marshal(c)
    if err != nil { return "", err }
    payload := base64.RawURLEncoding.EncodeToString(raw)
    mac := hmac.New(sha256.New, s.key[:])
    mac.Write([]byte(payload))
    return payload+"."+base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

func (s *MediaTicketSigner) Verify(r *http.Request, value string) (MediaTicketClaims, error) {
    var c MediaTicketClaims
    bad := errors.New("invalid or expired media ticket")
    if len(value) > 4096 { return c, bad }
    payload, signature, ok := strings.Cut(value, ".")
    if !ok { return c, bad }
    got, err := base64.RawURLEncoding.DecodeString(signature)
    if err != nil { return c, bad }
    mac := hmac.New(sha256.New, s.key[:])
    mac.Write([]byte(payload))
    if !hmac.Equal(got, mac.Sum(nil)) { return c, bad }
    raw, err := base64.RawURLEncoding.DecodeString(payload)
    if err != nil || json.Unmarshal(raw, &c) != nil { return c, bad }
    now := time.Now().Unix()
    if c.UserID <= 0 || c.ParentDigest == "" || c.Expires <= now ||
        c.Expires > now+300 || c.Method != r.Method || c.Target != mediaTicketTarget(r) {
        return c, bad
    }
    return c, nil
}
```
After signature verification, query the database before authenticating the request:
```sql
SELECT EXISTS (
  SELECT 1 FROM tokens
  WHERE user_id = ? AND value = ? AND revoked_at IS NULL
);
```
Bind the parameters to the verified user ID and parent digest. Fail closed on database errors. The signer is an ephemeral per-process key here: restart invalidates outstanding tickets, which is acceptable for short-lived URLs. Persist/rotate a protected key only if restart survival is required.

**Required integration.** Mint only for already authorized resources and allowed route/method combinations; populate `ParentDigest` from the authenticated parent token using `store.TokenDigest`, never from client JSON. Include semantically important query parameters in the target. HLS must mint separate tickets for each child target, not reuse an index-playlist ticket on segments. Plan refresh for long playback. Issue a distinct POST capability for one user's edition-progress endpoint. Log-redaction remains worthwhile even after scoping credentials.

**Regression test.** A ticket for one cover cannot authorize another path, a mutation, or a different user. Parent revocation invalidates it immediately; expiry fails closed; signatures/queries cannot be tampered with; playlists contain valid child tickets without exposing the parent bearer.

---

<a id="f30"></a>

### F30 — The token “last seen” throttle still executes a write statement on every authenticated request

**Severity: Low.** Source-confirmed performance risk; not benchmarked.

**Source:** [`internal/auth/auth.go`](https://github.com/libteca/libteca/blob/470a8edcc7e406088f71ac6e91ad99480d3f1aef/internal/auth/auth.go).

**Evidence and impact.** `LookupTokenUser` correctly rereads authentication state on every request, but then always executes an UPDATE with a last-seen age condition. The condition limits changed rows, not the number of UPDATE statements submitted to SQLite. Segment/image-heavy workloads can therefore perform many unnecessary write attempts. This is not a justification for restoring a positive authentication cache that could resurrect revoked tokens.

**Suggested fix.** Keep authoritative authentication reads. Avoid submitting the activity UPDATE when the selected last-seen time is already fresh, or use a bounded coalescing activity worker separate from authentication. Measure query count and write contention before claiming a specific throughput gain.

**Implementation — include last_seen_at in the existing token SELECT/Scan.**
```go
var lastSeen sql.NullInt64
// Append t.last_seen_at to the SELECT and &lastSeen to its Scan list.
now := time.Now().UnixMilli()
if !lastSeen.Valid || lastSeen.Int64 < now-60_000 {
    _, err := db.Exec(`UPDATE tokens SET last_seen_at = ?
        WHERE value = ? AND revoked_at IS NULL
          AND (last_seen_at IS NULL OR last_seen_at < ?)`,
        now, digest, now-60_000)
    if err != nil {
        slog.Warn("libteca: token activity update failed", "err", err)
    }
}
```
Keep the SQL-side condition to handle concurrent requests safely. This small change can still produce a burst of concurrent touches at the threshold; a bounded coalescer can reduce that further if measurement warrants it. Do not let activity logging failure turn a successfully verified token into an authentication success cache.

**Regression test.** Repeated valid requests within one minute should continue to perform token revocation reads while submitting few activity writes. Revoke between requests and verify immediate rejection regardless of activity-throttle state.

---

<a id="f31"></a>

### F31 — A failed transcoder can make an unfinished segment appear safe to serve

**Severity: Medium.** Source-confirmed readiness defect.

**Source:** [`internal/transcode/transcode.go`](https://github.com/libteca/libteca/blob/470a8edcc7e406088f71ac6e91ad99480d3f1aef/internal/transcode/transcode.go); [`internal/transcode/hwaccel.go`](https://github.com/libteca/libteca/blob/470a8edcc7e406088f71ac6e91ad99480d3f1aef/internal/transcode/hwaccel.go).

**Evidence and impact.** `WaitForSegment` treats a nonempty segment as ready when it is listed in the playlist **or** `s.isDead()` is true. `isDead()` means the process is finished with no fallback pending; it does not mean the exit was successful or that this segment was finalized. The command uses `independent_segments` but not `temp_file`, so the final segment filename can exist while still being written. A failed encoder can leave nonempty incomplete bytes that are then served as a successful segment.

**Suggested fix.** Require finalized publication, not merely process death. Use FFmpeg's temporary-file publication flag and serve only finalized segment files referenced by the playlist. Failed output should produce a recoverable playback error, not a 200 response containing an incomplete segment.

**Implementation.**
```go
// In buildArgs:
"-hls_flags", "independent_segments+temp_file",
```
```go
func (s *Session) segmentReady(index int) bool {
    name := fmt.Sprintf("seg%05d.ts", index)
    return fileReady(s.segmentPath(index)) && s.playlistLists(name)
}

// In WaitForSegment's loop, replace the readiness/death conditions:
if s.segmentReady(index) { return true }
if s.isDead() { return false }
```
Use finalized readiness in `Prebuffer` too. Make `playlistLists` compare actual URI lines, rather than arbitrary substrings, and bound playlist reads if accepting potentially very long sessions. A successful encoder exit alone should not bypass publication checks. Keep fallback-aware waiting, so an imminent software retry is not prematurely treated as final failure.

**Regression test.** Fake encoder writes nonempty segment bytes then exits with an error without listing the segment: readiness must be false. Verify successful final segment publication, process fallback, partial `.tmp` files, cancellation, and end-of-stream behavior.

---

<a id="f32"></a>

### F32 — Transcode cleanup holds the global manager lock during slow process/filesystem operations

**Severity: Medium.** Source-confirmed head-of-line blocking risk.

**Source:** [`internal/transcode/transcode.go`](https://github.com/libteca/libteca/blob/470a8edcc7e406088f71ac6e91ad99480d3f1aef/internal/transcode/transcode.go).

**Evidence and impact.** `Get`, `Close`, the reaper, and `CloseAll` call `s.kill()` while holding `Manager.mu`. `kill` can wait up to two seconds for process termination and then perform recursive deletion. During that time unrelated session lookup/touch/admission operations need the same manager lock. A slow cleanup can therefore stall unrelated viewers, and multiple cleanups compound the delay. Initial setup also performs filesystem/process work under the manager lock.

**Suggested fix.** Under the manager lock, reserve a lifecycle state; perform blocking cleanup outside it; remove the exact entry afterward. Keep closing entries counted against capacity and prevent directory/session-ID reuse until cleanup is actually finished. Simply deleting the map entry before cleanup would reintroduce a different lifetime race.

**Implementation — lifecycle pattern for `Close`.**
```go
// Add to Session: closing bool // guarded ONLY by Manager.mu
func (m *Manager) Close(sessionID string) {
    m.mu.Lock()
    s, ok := m.sessions[sessionID]
    if !ok || s.closing {
        m.mu.Unlock()
        return
    }
    s.closing = true
    m.mu.Unlock()

    if !s.kill() { // Change kill to report complete termination + cleanup.
        return // Keep the closing reservation; reaper retries it.
    }

    m.mu.Lock()
    if m.sessions[sessionID] == s {
        delete(m.sessions, sessionID)
    }
    m.mu.Unlock()
}
```
Change `kill()` to return `bool`: return false at its existing two-second process-exit timeout and on cleanup failure, and return true only after confirmed exit and successful directory removal. Its existing per-session lifecycle mutex serializes attempts. The reaper must collect **both** newly expired sessions and already-closing reservations and retry their finalization outside `Manager.mu`; calling the early-returning public `Close` on a closing reservation is not sufficient. Refactor its finalization into a shared helper or duplicate the pointer-checked final deletion shown above. `Get`, `Existing`, and segment admission must reject closing entries with a retryable response. The reaper should collect candidates under the lock and close them outside it; shutdown should join cleanup work. Initialization needs an analogous reserved “starting” entry so concurrent starts cannot exceed `MaxSessions`.

**Regression test.** Stall one fake process in cleanup and verify another session can be touched/looked up promptly. Concurrently close/reopen the same ID and verify no old cleanup deletes the new session's directory. Capacity must count still-running closing processes.

---

<a id="f33"></a>

### F33 — Session-count limits do not bound transcode disk consumption

**Severity: Medium.** Source-confirmed resource-policy gap; documented hardening area.

**Source:** [`internal/transcode/transcode.go`](https://github.com/libteca/libteca/blob/470a8edcc7e406088f71ac6e91ad99480d3f1aef/internal/transcode/transcode.go); [`internal/transcode/hwaccel.go`](https://github.com/libteca/libteca/blob/470a8edcc7e406088f71ac6e91ad99480d3f1aef/internal/transcode/hwaccel.go).

**Evidence and impact.** `MaxSessions` limits map entries, but output uses an unlimited HLS list and no explicit byte budget. A live session can retain all generated segments; several active sessions can exhaust the data filesystem despite staying below the session-count limit. Idle TTL does not protect against continuously touched sessions. No disk-exhaustion exploit was run during the audit.

**Suggested fix.** Define per-session and aggregate transcode byte budgets, free-space admission thresholds, and per-user fairness limits. Count starting/closing sessions until cleanup completes. Use a dedicated quota-controlled filesystem/project for a strict storage boundary; a periodic monitor alone has an overshoot window.

**Implementation — bounded directory accounting for a supervisor.**
```go
func directoryExceeds(dir string, limit int64) (bool, error) {
    if limit <= 0 { return false, fmt.Errorf("invalid transcode byte limit") }
    var total int64
    exceeded := false
    err := filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
        if err != nil { return err }
        if entry.IsDir() { return nil }
        info, err := entry.Info()
        if err != nil { return err }
        if !info.Mode().IsRegular() { return fmt.Errorf("unexpected transcode entry") }
        if info.Size() > limit-total {
            exceeded = true
            return filepath.SkipAll
        }
        total += info.Size()
        return nil
    })
    return exceeded, err
}

func monitorTranscodeBytes(ctx context.Context, s *Session, limit int64) error {
    tick := time.NewTicker(time.Second)
    defer tick.Stop()
    for {
        over, err := directoryExceeds(s.Dir, limit)
        if err != nil { return err }
        if over { return fmt.Errorf("transcode byte budget exceeded") }
        select {
        case <-ctx.Done(): return ctx.Err()
        case <-tick.C:
        }
    }
}
```
Start the monitor only after successful directory creation; unlike the current `Get`, check `os.MkdirAll` errors. Bind the context to the exact session. On a budget/accounting failure, stop that exact process, publish a clear capacity/storage error, and run tracked cleanup from F32. Add aggregate reservations/admission and a real filesystem quota for hard enforcement. Do not delete arbitrary still-advertised HLS segments without changing the playlist/player seek contract.

**Regression test.** Stay under the session-count limit while generating oversized output; verify quota rejection/termination and recovery after cleanup. Test simultaneous sessions crossing the aggregate threshold, accounting errors, and a process that has not exited after a kill request.

---

<a id="f34"></a>

### F34 — Edition-level playback selection is limited to the first file

**Severity: Medium.** Source-confirmed multipart API limitation; already recognized as architectural work.

**Source:** [`internal/api/core/hls.go`](https://github.com/libteca/libteca/blob/470a8edcc7e406088f71ac6e91ad99480d3f1aef/internal/api/core/hls.go); [`internal/api/core/core.go`](https://github.com/libteca/libteca/blob/470a8edcc7e406088f71ac6e91ad99480d3f1aef/internal/api/core/core.go).

**Evidence and impact.** `editionPlayback` selects `ed.Files[0]`, capability checks inspect that file, and `openEditionFile` also opens the first file. The web ticket stores user and edition but no selected file. The inspected edition-playback interface cannot select a later part of a multipart video edition. This report does not claim that every audio/protocol client lacks its own file sequencing; the limitation is specifically the inspected edition-level direct/HLS path.

**Suggested fix.** Represent playback as a selected file plus edition-wide offset. Validate requested file membership, bind the selection into the ticket, and map global seeks to a file/local offset. Advance to the next part on end-of-file. Avoid introducing unsafe FFmpeg concat path handling as a shortcut around the descriptor-confinement work.

**Implementation — validated selection helper.**
```go
func selectedPlaybackIndex(ed *store.EditionView, rawFileID string) (int, error) {
    if len(ed.Files) == 0 { return 0, store.ErrNotFound }
    if rawFileID == "" { return 0, nil }
    id, err := strconv.ParseInt(rawFileID, 10, 64)
    if err != nil || id <= 0 { return 0, fmt.Errorf("invalid playback file") }
    for i := range ed.Files {
        if ed.Files[i].ID == id { return i, nil }
    }
    return 0, store.ErrNotFound
}
```
Change capability checks/opening to use the selected index, and extend the server-side ticket with the selected immutable file ID and allowed start parameters. On child HLS requests, resolve that recorded selection rather than taking a new untrusted file query parameter.

**Implementation — browser edition-position mapping.**
```ts
export function locatePlaybackPart(
  files: readonly { id: number; duration: number }[], globalSeconds: number,
): { fileId: number; localSeconds: number; editionOffset: number } {
  if (!Number.isFinite(globalSeconds) || globalSeconds < 0 || files.length === 0)
    throw new Error("Invalid playback position");
  let base = 0;
  for (let i = 0; i < files.length; i++) {
    const f = files[i];
    if (!Number.isFinite(f.duration) || f.duration <= 0)
      throw new Error("Cannot map multipart seek with unknown duration");
    if (globalSeconds < base + f.duration || i === files.length - 1) {
      return { fileId: f.id,
        localSeconds: Math.min(f.duration, Math.max(0, globalSeconds - base)),
        editionOffset: base };
    }
    base += f.duration;
  }
  throw new Error("Playback position could not be mapped");
}
```
Progress must store `editionOffset + currentTime`, while seek/start passed to the selected file is local. Version the response contract and test compatibility faces independently. This is a feature/schema/API implementation, not a one-line safe hotfix.

**Regression test.** Direct and HLS playback across two files, seek across a boundary, resume in the second file, unknown durations, a file removed during playback, and a foreign file ID. Confirm the ticket cannot be reused to open a different edition or file.

---

<a id="f35"></a>

### F35 — The browser-playable predicate excludes VP8 even in its accepted WebM branch

**Severity: Low.** Source-confirmed boolean inconsistency.

**Source:** [`internal/api/core/hls.go`](https://github.com/libteca/libteca/blob/470a8edcc7e406088f71ac6e91ad99480d3f1aef/internal/api/core/hls.go).

**Evidence and impact.** The WebM container condition includes `vp8`, but the final video-codec condition admits only H.264, VP9, and AV1. A VP8 WebM with an otherwise accepted audio codec always fails the final predicate and is sent to transcoding. This is unnecessary server work for clients that can play the file directly; it can make such playback depend on FFmpeg/capacity even when direct playback would work.

**Suggested fix.** Make the branches consistent. Preserve container-specific restrictions rather than broadly declaring VP8 valid inside every accepted container. For more accurate heterogeneous-browser behavior, eventually use advertised client capabilities instead of a single server-wide codec list.

**Implementation — before the existing final return.**
```go
if vcodec == "vp8" {
    return strings.Contains(container, "webm") && audioOK
}
```
Retain the existing decision for other codecs. This resolves the internal inconsistency without claiming every browser supports every WebM/audio combination.

**Regression test.** VP8/WebM with accepted audio is direct; VP8 with a non-WebM container is not newly admitted; unsupported audio still causes fallback; existing H.264/VP9/AV1 cases remain unchanged.

---

<a id="f36"></a>

### F36 — CI uses mutable action tags and lacks an explicit dependency-security gate

**Severity: Low.** Source-confirmed supply-chain/process hardening opportunity.

**Source:** [`.github/workflows/ci.yml`](https://github.com/libteca/libteca/blob/470a8edcc7e406088f71ac6e91ad99480d3f1aef/.github/workflows/ci.yml); [`go.mod`](https://github.com/libteca/libteca/blob/470a8edcc7e406088f71ac6e91ad99480d3f1aef/go.mod); [`web/package.json`](https://github.com/libteca/libteca/blob/470a8edcc7e406088f71ac6e91ad99480d3f1aef/web/package.json).

**Evidence and impact.** The workflow uses mutable major action tags. It already runs Go vet/race tests, web tests, TypeScript checks, and a build; those protections should be retained. The inspected workflow does not run a dependency vulnerability check or formatting gate. These omissions are hardening opportunities, not evidence that an action or pinned dependency is compromised. No vulnerability inventory was successfully run in this audit environment.

**Suggested fix.** Pin actions to verified commit IDs, automate reviewed dependency updates, add formatting/dependency checks, and add the multi-account/multi-tab/reload/browser tests from this report. Do not substitute unit-test counts for end-to-end lifecycle coverage.

**Implementation — action pins resolved from the repository's existing major tags during this review.**
```yaml
# In both jobs where applicable:
- uses: actions/checkout@11d5960a326750d5838078e36cf38b85af677262 # v4 tag resolved at review
- uses: actions/setup-go@40f1582b2485089dde7abd97c1529aa768e1baff # v5 tag resolved at review
  with:
    go-version-file: go.mod
    cache: true
# In the web job:
- uses: actions/setup-node@49933ea5288caeca8642d1e84afbd3f7d6820020 # v4 tag resolved at review
  with:
    node-version: "22"
    cache: npm
    cache-dependency-path: web/package-lock.json
```
These are **resolved identities, not a security endorsement or a claim of latest versions**. The references were read through GitHub: [checkout v4](https://api.github.com/repos/actions/checkout/git/ref/tags/v4), [setup-go v5](https://api.github.com/repos/actions/setup-go/git/ref/tags/v5), [setup-node v4](https://api.github.com/repos/actions/setup-node/git/ref/tags/v4).

Add automatic update configuration:
```yaml
# .github/dependabot.yml
version: 2
updates:
  - package-ecosystem: github-actions
    directory: /
    schedule:
      interval: weekly
  - package-ecosystem: gomod
    directory: /
    schedule:
      interval: weekly
  - package-ecosystem: npm
    directory: /web
    schedule:
      interval: weekly
```
Additional job steps:
```yaml
# Go job:
- name: Check Go formatting
  run: test -z "$(gofmt -l cmd internal)"
# Web job (its existing working-directory is web):
- name: Audit runtime dependencies
  run: npm audit --omit=dev --audit-level=high
```
For Go vulnerability checking, add `golang.org/x/vuln/cmd/govulncheck` as a reviewed/pinned tool dependency using the repository's supported Go tool-dependency mechanism, commit the resulting module/sum changes, then run `go tool govulncheck ./...` in CI. Check development/build dependencies separately too; runtime-only npm auditing is not a full supply-chain audit. Do not insert an unpinned `@latest` tool download into every CI run.

**Regression gate.** Add store deletion-lineage tests, metadata-channel race tests, process-failure readiness tests, and real-browser account-switch/two-tab/offline-reload tests. Run fixtures against an isolated test server/database, never production. Record which media/corpus tests are skipped and why.

---

<a id="f37"></a>

### F37 — Atomic cover publication is not explicitly crash-durable

**Severity: Low.** Source-confirmed filesystem durability gap.

**Source:** [`internal/api/core/providers.go`](https://github.com/libteca/libteca/blob/470a8edcc7e406088f71ac6e91ad99480d3f1aef/internal/api/core/providers.go).

**Evidence and impact.** The cover download path writes to a temporary file, closes it, and renames it into place, but does not call `Sync` on the file or containing directory. Rename is useful for preventing readers from seeing a half-written file; it is not by itself a durable-write protocol. After a power loss, newly referenced cover bytes or the directory entry can be missing even though the operation previously returned success. This is lower severity than primary-media loss because many covers can be fetched again, but availability of the original remote image is not guaranteed.

**Suggested fix.** Sync the encoded file, close it, rename in the same directory, and sync the directory before committing the database reference. Use the same asset transaction lock as F25. Make the error path explicit when rename succeeded but the directory sync failed.

**Implementation — durable JPEG publication helper, also usable by F24.**
```go
func publishJPEG(dir, destination string, img image.Image) error {
    tmp, err := os.CreateTemp(dir, ".cover-*")
    if err != nil { return err }
    name := tmp.Name()
    defer os.Remove(name)
    if err := jpeg.Encode(tmp, img, &jpeg.Options{Quality: 90}); err != nil {
        tmp.Close()
        return err
    }
    if err := tmp.Sync(); err != nil {
        tmp.Close()
        return err
    }
    if err := tmp.Close(); err != nil { return err }
    if err := os.Rename(name, destination); err != nil { return err }
    directory, err := os.Open(dir)
    if err != nil { return err }
    syncErr := directory.Sync()
    closeErr := directory.Close()
    if syncErr != nil { return syncErr }
    return closeErr
}
```
Require `destination` to be an application-generated filename inside `dir`; do not expose it as an unrestricted user path. Apply a clearly documented platform policy if directory syncing is unsupported rather than silently claiming crash durability. Only publish/update the database reference after this helper succeeds. An error after rename can leave an orphaned valid file; that is safer than a durable reference to uncommitted bytes.

**Regression test.** Inject failures at encode/write, file sync, close, rename, and directory sync. Verify cleanup and that no successful database-reference update is reported before the durability steps finish. A real crash/power-loss test on supported filesystems is still needed to validate deployment-specific durability.

---

## Review coverage and explicit exclusions

The table below describes the actual source examined, not a claim that every file in each directory was reviewed.

| Area | Source inspected | Review depth |
|---|---|---|
| Reader progress | `web/src/progressQueue.ts`, `web/src/reader/shared.tsx`, `web/src/api.ts` | Queue, persistence, merge, lifecycle, transport, and API error handling; not every shared visual helper. |
| Readers | `web/src/reader/cbz.tsx`, `epub.tsx`, `pdf.tsx` | Loading, extraction, resume, progress posting, selected error/render paths; CBZ/EPUB were reviewed in relevant excerpts. |
| Progress store | `internal/store/reading.go`, `internal/store/progress.go` | Full returned source, conditional/unconditional writers, deletion, readers, session-close progress. |
| Backup store | `internal/store/backup.go` | Full returned source: lock, snapshot, copy, publication, retention. |
| Authentication | `internal/auth/auth.go` | Full returned source: password/token helpers, database checks, middleware, token activity. |
| Core API | `internal/api/core/core.go` | Selected scan-event/read/work/progress/media-serving sections, not the whole API file. |
| Core playback | `internal/api/core/hls.go` | Playback selection/capability checks, tickets, thumbnail entry points, playlist rewriting. |
| Metadata | `internal/api/core/providers.go` | Optional apply handling, chapter distribution, cover download/publication, metadata-run SSE lifecycle and status paths. |
| Transcoding | `internal/transcode/transcode.go`, `hwaccel.go` | Manager/session lifecycle, readiness, cleanup, capacity, encoder/HLS arguments and hardware selection. |
| ABS compatibility | `internal/api/abs/abs.go` | Route/login excerpt, including exposed progress deletion; not a full ABS protocol audit. |
| Build and CI | `go.mod`, `web/package.json`, `.github/workflows/ci.yml` | Declared requirements, scripts, dependencies and workflow steps; no vulnerability result inferred from version strings. |
| Context | Repository trees, pinned commit metadata, `AUDIT_OPEN.md` excerpts | Orientation and avoiding automatic repetition of already-resolved historical findings. |

The prior source browsing also retrieved tree listings and file metadata, but a tree listing is not equivalent to reviewing the corresponding implementation. Scanner internals, importer bodies, all podcast service paths, all OPDS/Subsonic/Jellyfin routes, WebSocket behavior, full audio/video player state machines, full migrations and historical-data upgrades, deployment/runtime sandbox configuration, third-party parser source, and the full test suite remain outside comprehensive verification here.

No credentials were requested or used to access a running libteca deployment. No live-target exploitation, destructive production tests, load tests, GPU/FFmpeg hardware tests, complete database migration/restore tests, real-browser cross-device tests, or filesystem crash/power-loss tests were performed. The absence of a finding in one of those areas is not a clean bill of health.

## Relationship to previous audits and intended behavior

The repository contains earlier audit reports and an open-items register. This report does not copy all their old findings into the current issue count. In particular, the presence of a generation backup layout, token digesting, conditional reader writes, explicit presence masks, rooted media openers, and the current CI checks was taken into account. The remaining concerns must be evaluated against the pinned implementation, not against an earlier audit's original code.

Some findings deliberately revisit an **accepted tradeoff or incomplete feature**: unconditional beacons (F06), progress-bound policy (F14), browser/archive resource budgets (F17), coherent database/assets snapshots (F25), URL-token scope/lifetime (F29), transcode storage budgets (F33), and multipart playback selection (F34). Their inclusion means that the risk or limitation remains relevant, not that the maintainers never considered it. The original scope of a historical finding may differ from this report's narrower residual issue.

Avoid these tempting but incorrect remediation shortcuts: do not restore positive token-auth caches; do not blindly max-merge explicit user resets; do not attach a fresh revision to an old stored operation; do not treat a queued beacon as an acknowledgement; do not release capacity/session names before old process cleanup; do not replace descriptor-confined media opening with an unsafe concat pathname; and do not prune good backups after a failed completeness check.

## Reproductions executed in this audit

These programs intentionally isolate specific semantics. The SQL schema is a reduced model of the relevant progress key/revision columns. The JavaScript is a direct transcription of the reviewed merge function plus small event schedules. The Go channel program inserts a hook at an unlocked point solely to select an interleaving that the original method permits; it is not a claim that the exact repository test suite was compiled.

### SQL condition and deletion-lineage check

```python
import sqlite3, json
con = sqlite3.connect(':memory:')
con.execute('CREATE TABLE progress(user_id INTEGER, edition_id INTEGER, page INTEGER, revision INTEGER NOT NULL, PRIMARY KEY(user_id, edition_id))')
def write(page, base):
    return con.execute('''INSERT INTO progress VALUES (1,1,?,1)
    ON CONFLICT(user_id,edition_id) DO UPDATE SET page=excluded.page, revision=progress.revision+1
    WHERE progress.revision=? RETURNING revision''',(page,base)).fetchone()
print('SQLite', sqlite3.sqlite_version)
print('Absent row, base=99:', write(7,99))
print('Existing row, stale base=99:', write(8,99))
con.execute('DELETE FROM progress')
write(90,0)
con.execute('DELETE FROM progress')
write(0,0)
print('ABA stale base=1:', write(91,1), 'row:', con.execute('select page, revision from progress').fetchone())
```

Observed output:

```text
SQLite 3.46.1
Absent row, base=99: (1,)
Existing row, stale base=99: None
ABA stale base=1: (2,) row: (91, 2)
```

### Merge and storage event-schedule checks

```javascript
// Direct JavaScript transcription of the fetched TypeScript merge function.
function mergeServerProgress(server, patch) {
  const merged = {};
  let localWins = true;
  if (patch.page !== undefined) {
    if (server.page !== undefined && server.page >= patch.page) localWins = false;
  } else if (patch.percent !== undefined) {
    if (server.percent !== undefined && server.percent >= patch.percent) localWins = false;
  }
  if (localWins) {
    if (patch.page !== undefined) merged.page = patch.page;
    if (patch.percent !== undefined) merged.percent = patch.percent;
  }
  if (patch.locator !== undefined && (localWins || (patch.page === undefined && patch.percent === undefined))) merged.locator = patch.locator;
  if (patch.finished !== undefined) merged.finished = patch.finished;
  return merged;
}
console.log('Locator-only conflict:', JSON.stringify(mergeServerProgress({revision:7,percent:0.9,locator:'far'},{locator:'early'})));
let server={revision:7,page:90};
const patch={page:25};
console.log('First conflict merge:', JSON.stringify(mergeServerProgress(server,patch)));
let clientRevision=server.revision; // exactly the assignment in the sender
const nextPatch={page:26};
if(clientRevision===server.revision) server={...server,...nextPatch,revision:server.revision+1};
console.log('Next page accepted after discarded conflict:',JSON.stringify(server));
let stored={page:20};
const queueA={...stored};
stored={page:90}; // B persists a new pending patch
stored={}; // A success: persist({}) removes storage entry
console.log('Shared-key pending patch after another queue ack:',JSON.stringify(stored));
```

Observed output:

```text
Locator-only conflict: {"locator":"early"}
First conflict merge: {}
Next page accepted after discarded conflict: {"revision":8,"page":26}
Shared-key pending patch after another queue ack: {}
```

### Metadata channel and truncated-image checks

```go
package main
import (
 "bytes"
 "fmt"
 "image"
 "image/png"
 "sync"
)
type metaSnap struct{ Status string }
type metaRun struct{ mu sync.Mutex; snap metaSnap; subs map[chan metaSnap]struct{}; closed bool }
func (r *metaRun) subscribe(interleave func()) chan metaSnap {
 ch:=make(chan metaSnap,8); r.mu.Lock(); s:=r.snap
 if r.closed {r.mu.Unlock(); ch<-s; close(ch); return ch}
 if r.subs==nil {r.subs=map[chan metaSnap]struct{}{}}
 r.subs[ch]=struct{}{}; r.mu.Unlock()
 interleave() // Audit-only hook selecting an interleaving allowed by the original code.
 ch<-s; return ch
}
func(r *metaRun) finish(s metaSnap){
 r.mu.Lock();r.snap=s;r.closed=true;subs:=r.subs;r.subs=nil;r.mu.Unlock()
 for ch:=range subs {select{case ch<-s:default:};close(ch)}
}
func reproducePanic(){
 defer func(){fmt.Printf("Metadata subscribe/finish interleaving: %v\n",recover())}()
 r:=&metaRun{snap:metaSnap{"running"}}
 r.subscribe(func(){r.finish(metaSnap{"done"})})
}
func main(){
 reproducePanic()
 var b bytes.Buffer
 if err:=png.Encode(&b,image.NewNRGBA(image.Rect(0,0,1,1)));err!=nil{panic(err)}
 data:=b.Bytes();truncated:=data[:len(data)-12]
 _,format,configErr:=image.DecodeConfig(bytes.NewReader(truncated))
 _,_,decodeErr:=image.Decode(bytes.NewReader(truncated))
 fmt.Printf("PNG without IEND: format=%s DecodeConfig=%v Decode=%v\n",format,configErr,decodeErr)
}
```

Observed output:

```text
Metadata subscribe/finish interleaving: send on closed channel
PNG without IEND: format=png DecodeConfig=<nil> Decode=unexpected EOF
```

## Required verification before deploying the proposed changes

Use the repository's declared Go toolchain and an isolated fixture database/media directory. Preserve a verified backup before running new schema migrations. After integrating the reference changes, the baseline commands are:

```sh
go vet ./...
go test ./... -count=1 -timeout 600s
go test -race ./... -count=1 -timeout 600s
(cd web && npm ci && npx tsc --noEmit && npm run test && npm run build)
```

Those commands alone do not establish the lifecycle/durability guarantees discussed above. Add real-browser tests for two users on one browser, two tabs on one account, account changes during in-flight requests, offline/reload replay, stale beacons, PDF saves, restored EPUB indexes, permanent validation failures, and both stalled headers and stalled response bodies. Add store tests for every progress writer and reader, tombstones, conditional creation, concurrent CAS failures, and operation-ID replay/reuse. Add process/FS tests for partial encoder output, slow termination, cleanup retry, asset snapshot coordination, missing sources, abandoned stages, and write/sync failures.

The highest-value acceptance assertions are: no request is sent under the wrong account; a stored operation never acquires a new baseline without reconciliation; deletion cannot reset concurrency identity; an uncertain retry cannot repeat an already-applied completion/reset; metadata subscriptions cannot send on closed channels; incomplete media is never advertised as ready; and retention never removes older good generations after a failed backup-completeness check.

For compatibility faces that intentionally remain last-writer-wins, make the limitation explicit and test their interaction with conditional readers. For assets and media that are treated as administrator-trusted rather than hostile input, document that trust boundary and the limits of parser/resource confinement. Do not describe reference code as production-verified until these tests have actually been run and their results recorded.

## Primary reference notes

Each finding links directly to the pinned GitHub source files above. Additional platform behavior was checked against the following primary documentation:

- [SQLite UPSERT](https://www.sqlite.org/lang_upsert.html): the `DO UPDATE` condition governs conflict handling, not an unrelated successful insert; this supports the reduced SQL check in F05.
- [MDN: Navigator.sendBeacon](https://developer.mozilla.org/en-US/docs/Web/API/Navigator/sendBeacon): queued transport and the absence of a normal response-handling path should not be confused with application acknowledgement; this informs F06's design.
- [FFmpeg format documentation](https://ffmpeg.org/ffmpeg-formats.html): HLS temporary-file publication is relevant to F31. Test behavior against the FFmpeg build actually deployed.

The action commit IDs in F36 were resolved through GitHub at review time. This report does not assert that they are the latest action releases or that pinning alone validates their contents.

---

**End of report.** All findings in this document refer to the pinned revision unless explicitly identified as proposed implementation or externally verified platform behavior. No complete-repository, production-security, or deployability guarantee is implied.
