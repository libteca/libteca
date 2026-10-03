# Proposed media intent and recovery contract

Status: executable design fixtures only. `web/src/contracts/mediaIntent.ts` is a pure reference model exercised by `web/tests/mediaIntentContract.test.ts`. No player, API handler, storage adapter, or production entry point imports it. It does not implement durable browser storage, transport, replay ownership, a server receipt table, or podcast migrations. The in-tab audio delivery currently shipped remains unconditional at the server and does not survive tab/process loss.

This document elaborates section 2 of [the media consistency proposal](media-consistency-proposal.md). It does not supersede the reader-specific maximum-position policy in DECISIONS 42–43 or change legacy compatibility writes. A reader merge must not be reused for media seeks, restart, completion, or reset.

## Model vocabulary and assumptions

An immutable version-1 operation contains an operation ID, owner ID, target kind and ID, installation ID, playback-session ID, local intent epoch, sequence, timeline generation, original base revision, intent, and complete file-ID/file-offset/cumulative-position tuple. There are no credentials, authentication tokens, wall-clock ordering fields, or token-derived storage keys. The in-memory login-generation fence is omitted from recovery records.

- Owner plus target kind plus target ID identifies progress. Edition and podcast episode namespaces never alias.
- A sequence increases within one installation/playback session. An explicit seek, restart, or reset starts a newer intent epoch. A fresh heartbeat after that choice belongs to the new epoch. Another device/session is not considered causally ordered by these counters.
- `seek` preserves the chosen tuple, including backward movement. `restart` uses the beginning tuple and clears completion. `finish` records explicit completion at its supplied valid tuple. `reset` stores the beginning tuple as a tombstone. Only terminal-file playback should automatically emit `finish`; a separate manual mark-finished policy remains a product decision.
- Each successful conditional write increments the revision by exactly one. Revision zero represents an absent row in fixtures; a reset retains a positive revision. This follows the existing edition CAS shape but is not yet a podcast contract.
- Server snapshots and acknowledgements must be authoritative. The model's optional `appliedOperation` is hypothetical last-write evidence containing the exact immutable operation. It is not a claim that current endpoints return it or that a receipt table exists. A current value alone cannot prove which device produced it.
- Position shape checks reject negative/non-finite values and offsets greater than cumulative position. These checks do not prove file membership or physical validity. Admission must additionally validate the complete tuple against the authoritative timeline before any real write. The reference apply function assumes that admission has occurred.
- Domain identifiers here are opaque strings. The separate multipart timeline contract uses its own API-facing identifiers and can represent unknown durations. No implicit casting or import connects them. An adapter must deliberately map the established numeric API IDs, namespace targets, preserve identity, and handle unknown durations before integration.

## Conflict decision table

Rules are applied in the order below. A conflict retains the operation and needs a visible decision or a separately defined reconciliation step. No row selects the maximum position across explicit intents or independent sessions.

| Condition | Reference decision | Required behavior |
| --- | --- | --- |
| Invalid operation/snapshot, owner/target mismatch, or regressed revision | Conflict | Keep separate ownership/target scope; do not transmit or manufacture a base |
| Generation differs | Conflict | Refetch timeline; do not apply a stale cumulative position |
| Snapshot identifies the same operation ID but payload differs | Conflict | Treat as reused identity; do not call it an acknowledgement |
| Exact operation proof, expected value, and revision equal to original base + 1 | Committed | Retire that operation only; no further write |
| Matching base and explicit seek/restart/finish/reset | Apply exact tuple | Backward seek/restart is intentional; reset creates a tombstone |
| Matching base heartbeat against completed/tombstoned state | Conflict | A heartbeat cannot reopen or resurrect |
| Matching base heartbeat moves backward, repeats a stale sequence, or predates a known local epoch | Conflict | Do not disguise a seek as playback advancement |
| Matching base ordinary forward heartbeat | Apply exact tuple | Preserve the full tuple; CAS still decides at commit |
| Stale base against a reset tombstone | Conflict | Never recreate from zero or an older positive base |
| Stale explicit intent, including an equal-value current state without exact operation proof | Conflict | Show the local choice and current remote state; do not silently reassert either |
| Stale heartbeat against another session/device/epoch or a non-heartbeat last write | Conflict | Neither remote completion nor a newer seek/restart is overwritten |
| Stale heartbeat already covered by a later same-generation/same-lineage heartbeat | Covered | Keep the complete remote tuple; settlement means semantic coverage, not proof this operation committed |
| Stale heartbeat strictly newer in sequence and farther forward than a same-lineage heartbeat | Fresh-heartbeat candidate | Return the current base as a proposal; retain the original operation unchanged and require a fresh operation ID plus persistence before transmission |
| Sequence/position ordering disagrees, or equal cumulative values carry incompatible tuples | Conflict | Do not guess a winner or mix fields from competing tuples |

`decideMediaIntent` expresses these rules. `applyMediaIntent` simulates only a matching-base atomic application and returns a new snapshot. It returns null for every other decision; it does not silently execute a heartbeat rebase. `covered` and `committed` are distinct outcomes.

Exact same-base race example: two devices submit a seek and a finish against revision 7. Whichever request actually commits obtains revision 8; the other must see a conflict. The fixtures do not nominate device order, client timestamps, or a higher position as the winner.

## Persistence and delivery transition table

All events are inert descriptions supplied to pure functions. There is no network request, browser event handler, storage access, timer, or authentication implementation in this module.

| State/event | Next state | Ordering contract |
| --- | --- | --- |
| Stage a valid operation | Staged | Copy the complete operation and original base |
| Initial persistence succeeds | Queued | Transmission can now be considered |
| Initial persistence fails, including quota/denial | Persistence-failed | Do not transmit or claim durable protection; expose failure |
| Queued while offline or another tab owns replay | Queued | Retain unchanged, without a transport attempt |
| Queued with wrong/no owner or unavailable login identity | Blocked | Do not send through a replacement account |
| Queued with a different generation | Conflict | Refetch/reconcile timeline before any delivery |
| Queued, online, correct owner/generation, replay owner | Sending | Capture a volatile owner/login-generation fence |
| Sending with a valid same-fence acknowledgement | Settled/committed | Ack must identify this operation and original base + 1 |
| Timeout, unreadable/lost response, or invalid acknowledgement | Uncertain | Preserve operation; read/reconcile before another attempt |
| Transport proves no application occurred | Queued | Retry the identical operation/base; a generic 5xx does not by itself prove this |
| Account or login generation changes while sending | Uncertain | Invalidate the old fence; ignore late old-login responses |
| Timeline changes during delivery, even with an otherwise valid ack | Uncertain | Retain old-generation evidence; do not advance a new-timeline UI from it |
| Browser/process restart with a supported stored record | Uncertain | Always reconcile first, even if the record last appeared merely queued |
| Reconciliation still shows original base and permits operation | Queued | Retry original payload, identity, and base, never relabel with a fresh GET revision |
| Reconciliation proves commit or same-lineage heartbeat coverage | Settled | Record which outcome occurred |
| Reconciliation exposes conflicting/newer state | Conflict | Retain and show the choice; never overwrite through a guessed rebase |
| Newer local explicit intent supersedes a queued older operation | Settled/superseded | Replacement must already be successfully persisted and queued |
| Supersede request targets sending/uncertain work or an unpersisted replacement | Unchanged | Preserve potentially applied work until reconciliation |
| Unknown/invalid stored version or malformed operation | Quarantine | Preserve for inspection/explicit policy; do not silently import, send, or delete |

Persistence ordering is deliberate: first commit the replacement record, then retire a safely superseded predecessor. Failure to persist the replacement must leave the old record recoverable. Runtime storage must make the retirement/replacement relationship atomic or preserve enough causal metadata to recover a crash between those steps. The pure `supersede` transition proves only the precondition, not an actual transaction. Recovered records are uncertain, so the model does not drop one merely because a newer record exists.

A caller must dispatch the identity-change event when a token is replaced, including replacement for the same owner. Context comes from current verified identity, never from the stored record. Returning to the original account requires a fresh read under the new login generation. These are delivery fences, not an authentication or security assessment.

Before a newer explicit choice is sent, a runtime coordinator must settle or surface any potentially applied predecessor and construct the new choice against a confirmed base. The model does not implement a multi-operation queue, leadership lease, cross-tab lock, storage transaction, observer UI, retry scheduler, or record deletion. In particular, calling the pure apply helper separately for two same-base operations is not a simulation of an atomic server race; a race fixture must feed the winner's returned state to the loser.

`mediaRecoveryRecord` creates a copied serializable envelope only after persistence has succeeded. `restoreMediaIntent` accepts a supported valid envelope and returns uncertain state. Tests round-trip ordinary JSON in memory. They do not establish storage availability, eviction resistance, crash durability, or exactly-once delivery.

## Generation reconciliation

`rebaseMediaGeneration` is a proposal generator, not a mutation or acknowledgement. It accepts old and new complete, known-duration timelines with unique file IDs and a caller-proven unchanged content identity. It checks the old tuple, keeps the exact file-relative offset, and derives the new cumulative position from new order. A successful result does not change the original operation's ID, base, generation, or delivery state.

This domain helper intentionally refuses missing/replaced files, changed durations, duplicate IDs, absent identities, non-finite/zero durations, and incompatible generation evidence. The `contentIdentity` string is a proof input, not a newly invented production hash or proof source. A filename, matching duration, title, or unchanged file ID alone is insufficient evidence of unchanged content. Actual identity provenance and invalidation remain to be chosen.

Seek and heartbeat tuples may be mapped after an order-only change when those conditions hold. Restart/reset refer to a global beginning and finish may refer to a former ending; they require a new choice after generation change. Uncertain old-generation delivery must first be reconciled or surfaced. Integration must then construct and persist a new operation against the current revision; it must not mutate and replay the old envelope. Unknown-duration timelines need explicit adapter behavior rather than guessed offsets.

## Remaining decisions and acceptance gates

1. Authoritative generation and identity provenance: which server fields prove unchanged bytes and ordering, when revisions/generations change, and how scans, imports, replacements, and historical playback sessions interact.
2. Server evidence for ambiguous writes: exact last-operation metadata, optional receipts and their retention, or a visible unresolved-choice flow. Current edition revision CAS alone cannot prove that an equal-value explicit write was ours. Receipt-table policy previously rejected for reader maximum-merge is not automatically valid for media.
3. Persistent schema and ownership: storage engine, migration/version/quarantine policy, record limits, quota/denial UI, retirement atomicity, cross-tab replay-owner coordination and fallback, and eviction/restart behavior.
4. Conflict UI and user choice: keep remote versus issue a fresh explicit local choice, all outstanding dependent operations, and whether some acknowledged old-generation outcomes can be retired independently of current playback state.
5. Intent production: playback-session and epoch lifetimes, sequence allocation across tabs, explicit mark-finished versus final-file completion, and new playback after tombstones. Do not infer a seek from a lower heartbeat.
6. API adapters: map domain/string IDs to existing API identifiers, apply authoritative tuple validation including duration uncertainty, add podcast revisions/tombstones, define acknowledgement fields, and retain the documented unconditional legacy-face path until explicitly upgraded.
7. Real acceptance: offline pause/seek/restart/finish, pagehide/beacon ambiguity, browser restart and eviction, two tabs/devices, account/token replacement, reorder/deleted/replaced files, and actual native audio/video behavior. Pure fixtures do not close Gate W, real-browser/device, corpus, migration, or deployment gates.

## Verification

The focused suite contains 72 deterministic tests. It covers all five intents, backward movement, completion/restart ordering, tombstones/absence, same-session heartbeat coverage, two-session/device conflicts, stale sequence/epoch, owner/target isolation, podcast-shaped fixtures, persistence failure, replay ownership, acknowledgements, ambiguous responses, restart before/after an attempted write, account/login replacement, in-flight generation change, replacement persistence ordering, unsupported recovery versions, immutable snapshots, and unchanged-file generation mapping.

Run from the repository root:

```sh
./web/node_modules/.bin/vitest run --root web tests/mediaIntentContract.test.ts
./web/node_modules/.bin/tsc --noEmit -p web/tsconfig.json
```

Both passed locally for this preparation. The tests exercise a hypothetical contract only; no production data, storage schema, runtime player behavior, server route, credential, network service, or deployed build was changed by these three files.
