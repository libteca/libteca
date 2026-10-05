# libteca audit follow-up after audit10/audit11

Date: 2026-10-04  
Audited revision: `2fdef077780aa3c837cd5787690caf8a62258202` (`main`, audit11 landing)  
Audit10 revision: `a15c17f147448bf91de8a9c40ae515d9b021592c`  
Repository: `libteca/libteca`

## Scope and exclusions

I read `AUDIT_OPEN.md` before auditing the new code, then reviewed the audit10 and audit11 changes and the adjacent authentication, core-media, ABS progress/session, importer, server, and store code.

This report intentionally does **not** re-report findings already marked fixed in `AUDIT_OPEN.md`, nor the standing deferred architecture families (multipart media, source identity, content generations, durable/offline media intent, measured transcode/trickplay byte budgets, Subsonic compatibility secret migration, scanner identity, and the other explicitly recorded deferrals).

The findings below are limited to regressions or implementation gaps visible on current `main`.

## Executive summary

I found **3 actionable findings** in the newly touched surfaces:

| ID | Severity | Area | Finding |
|---|---|---|---|
| A12-01 | Medium | auth/core/web | The new media cookie is the account bearer token and is accepted for state-changing reader progress POSTs; `SameSite=Strict` is not a complete CSRF boundary for same-site/cross-origin deployments |
| A12-02 | Medium | ABS | Session sync/close validates `currentTime` against the client-supplied duration before replacing zero with the authoritative edition duration, allowing impossible positions |
| A12-03 | Medium | ABS/store | `timeListened` only receives a negative check, so arbitrarily large finite deltas can poison session accounting; this contradicts the previously recorded shared numeric-validation contract |

I did not find a new actionable defect in audit11's KDF-busy classification, importer entropy propagation, transaction rollback behavior, or explicit secure-cookie flag after reviewing those paths.

---

## A12-01 — media cookie authorizes a mutation and contains the full account bearer token

**Severity:** Medium security / privilege-boundary hardening  
**Confidence:** High  
**Files:** `internal/auth/auth.go`, `internal/api/core/mediaauth.go`, `internal/api/core/core.go`, `web/src/reader/shared.tsx`

### Evidence

audit10 removed bearer tokens from media URLs by setting an HttpOnly cookie whose value is the user's ordinary account bearer token:

```go
func SetMediaCookie(w http.ResponseWriter, value string, secure bool) {
    http.SetCookie(w, &http.Cookie{
        Name:     MediaCookieName,
        Value:    value,
        Path:     "/api/core",
        HttpOnly: true,
        SameSite: http.SameSiteStrictMode,
        Secure:   secure,
    })
}
```

The media-route classifier includes the whole progress prefix without considering the HTTP method:

```go
case strings.HasPrefix(p, "/progress/"):
    return true
```

and core registers both:

```go
r.HandleFunc("GET /progress/{editionId}", a.getProgress)
r.HandleFunc("POST /progress/{editionId}", a.setProgress)
```

The frontend deliberately depends on cookie authentication for lifecycle writes because `navigator.sendBeacon` cannot attach the bearer header:

```ts
const url = media(`/progress/${editionId}`);
navigator.sendBeacon(url, new Blob([payload], { type: "application/json" }));
```

So the new cookie is not merely a read-only media credential: it can authorize a state-changing POST.

`SameSite=Strict` blocks ordinary *cross-site* cookie delivery, but it is not an origin check. Same-site/cross-origin requests can exist, for example between sibling hosts under a shared registrable domain. A hostile same-site origin can submit a form POST to the libteca origin without CORS permission; the target host still receives its host-only cookie, and SameSite can regard the request as same-site. The mutation therefore should not rely on SameSite alone as its CSRF boundary.

There is a second containment issue: the cookie value is the general account token and its Path is `/api/core`, so the browser sends that high-value credential on every request below `/api/core`, even though middleware consumes it only on routes classified as media.

`AUDIT_OPEN.md` already notes that short-lived scoped media tickets are the deeper credential-lifecycle redesign. This finding does **not** reopen the broader deferred token-lifecycle work: the immediate bug is that the newly introduced cookie-auth path crosses from passive media delivery into a mutation without a request-origin defense.

### Fix

Keep the existing cookie for compatibility if necessary, but do not let cookie fallback alone authorize unsafe methods.

A narrow immediate fix is:

1. treat cookie fallback as read-only for media endpoints;
2. add an explicit origin/fetch-metadata guard for the one beacon mutation that must use a cookie;
3. preferably issue a separate narrowly scoped progress-beacon capability instead of reusing the full bearer token.

For example:

```go
func MediaRequest(r *http.Request) bool {
    if r.Method != http.MethodGet && r.Method != http.MethodHead {
        return false
    }
    return isMediaPath(r.URL.Path)
}
```

Then mount the progress beacon through a dedicated middleware:

```go
func SameOriginMutation(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        if r.Method == http.MethodPost {
            if site := r.Header.Get("Sec-Fetch-Site"); site != "" &&
                site != "same-origin" && site != "none" {
                http.Error(w, "forbidden", http.StatusForbidden)
                return
            }

            if origin := r.Header.Get("Origin"); origin != "" {
                expected := requestOrigin(r) // from trusted server config, not arbitrary forwarded headers
                if origin != expected {
                    http.Error(w, "forbidden", http.StatusForbidden)
                    return
                }
            }
        }
        next.ServeHTTP(w, r)
    })
}
```

Do **not** derive the expected origin from untrusted `X-Forwarded-*` headers unless a trusted-proxy configuration exists.

A stronger contained design is a separate random cookie/capability for media/beacon traffic:

```text
account bearer token
    -> explicit Authorization header only

media capability
    -> random opaque value
    -> server-side digest maps to user id
    -> limited to GET/HEAD media + POST /progress/*
    -> short TTL / revocable with parent login token
```

That prevents a media-cookie disclosure from becoming disclosure of the general API credential and makes its authority auditable.

### Regression tests

Add tests that prove:

- cookie-only `GET /stream/...`, covers, HLS, download, SSE still work;
- cookie-only POST to ordinary core mutations is rejected;
- progress beacon POST rejects `Sec-Fetch-Site: cross-site`;
- progress beacon POST rejects a mismatched `Origin`;
- same-origin beacon POST succeeds;
- query tokens remain rejected on media paths;
- bearer-header progress POST remains compatible.

If the scoped media capability is implemented, also assert it cannot authenticate `/me`, user/token administration, library mutation, or other ordinary APIs even when copied into an Authorization header.

---

## A12-02 — ABS sync/close validates against the wrong duration when clients send zero

**Severity:** Medium data integrity / protocol correctness  
**Confidence:** Certain  
**File:** `internal/api/abs/abs.go`

### Evidence

Both `sessionSync` and `sessionClose` validate the position before replacing a zero client duration with the edition's real duration.

Current `sessionSync`:

```go
if err := store.ValidPosition(body.CurrentTime, body.Duration); err != nil {
    fail(w, 400, err.Error())
    return
}

ed, err := a.DB.EditionByID(s.EditionID)
...
dur := body.Duration
if dur == 0 {
    dur = ed.TotalDuration()
}
```

`sessionClose` has the same ordering.

`store.ValidPosition(position, 0)` intentionally treats the duration as unknown and permits positions up to the 30-day unknown-duration policy ceiling. Therefore, for a 60-second edition a client can send, for example:

```json
{
  "currentTime": 100000,
  "duration": 0,
  "timeListened": 1
}
```

and pass the first validation. The handler later changes `dur` to the real 60 seconds, writes a progress row whose position is far beyond that duration, and marks it finished.

This is inconsistent with the core progress endpoint, which already validates positions against `ed.TotalDuration()` rather than a client duration. `AUDIT_OPEN.md` records that server-known edition duration as the intended progress-validation policy.

The issue existed in the adjacent ABS path before audit11, but audit11 directly refactored the sync/close transaction path and preserved the bad ordering, so this is a missed invariant in the just-landed code.

### Fix

Load the edition first, resolve the effective duration, then validate against the authoritative bound.

```go
ed, err := a.DB.EditionByID(s.EditionID)
if err != nil {
    if errors.Is(err, store.ErrNotFound) {
        fail(w, 404, "Session not found")
    } else {
        serverError(w, r, err)
    }
    return
}

dur := body.Duration
if serverDur := ed.TotalDuration(); serverDur > 0 {
    // The library metadata is authoritative whenever it is known.
    dur = serverDur
}

if err := store.ValidPosition(body.CurrentTime, dur); err != nil {
    fail(w, 400, err.Error())
    return
}

fileID, offset := ed.Locate(body.CurrentTime)
p := &store.Progress{
    UserID: s.UserID,
    EditionID: s.EditionID,
    FileID: &fileID,
    FileOffsetSecs: offset,
    EditionPositionSecs: body.CurrentTime,
    DurationSecs: &dur,
    ...
}
```

Using server duration whenever it is known is preferable to trusting a nonzero client duration too. Otherwise a client can send an exaggerated nonzero duration and obtain the same bypass:

```json
{"currentTime":100000,"duration":200000}
```

The core endpoint has already established the safer rule: **client duration can be metadata, but it must not be the position authorization bound when the server knows the edition duration.**

For an edition whose server duration is genuinely unknown/zero, retain `store.ValidPosition(..., 0)` and its 30-day fallback limit.

Apply the same helper/order to both `sessionSync` and `sessionClose` to prevent drift:

```go
func sessionDuration(ed *store.EditionView, client float64) float64 {
    if d := ed.TotalDuration(); d > 0 {
        return d
    }
    return client
}
```

### Regression tests

For both sync and close:

- seed a 60-second edition;
- `currentTime=70, duration=0` -> 400 and no session/progress mutation;
- `currentTime=70, duration=1000` -> 400 and no mutation;
- `currentTime=60, duration=0` -> accepted;
- unknown server duration + bounded client position remains accepted;
- failure leaves both the progress row and session row unchanged.

These should sit next to the audit11 atomicity/body-contract tests so the transaction and numeric invariants are tested together.

---

## A12-03 — ABS `timeListened` accepts unbounded finite deltas

**Severity:** Medium data integrity  
**Confidence:** Certain  
**Files:** `internal/api/abs/abs.go`, `internal/store/progress.go`

### Evidence

`store.ValidPosition` implements the repository's shared numeric policy:

```go
func ValidPosition(position, total float64) error {
    if math.IsNaN(position) || math.IsInf(position, 0) || position < 0 {
        return fmt.Errorf("invalid position")
    }
    ...
    if position > 30*24*60*60 {
        return fmt.Errorf("position exceeds policy limit for unknown duration")
    }
    return nil
}
```

But current sync and close only reject a negative `timeListened`:

```go
if body.TimeListened < 0 {
    fail(w, 400, "invalid timeListened")
    return
}
```

The transactional store helpers then add the unchecked value:

```sql
time_listened_secs = time_listened_secs + ?
```

A JSON number such as `1e300` is finite and decodes into `float64`, so an authenticated ABS client can inflate its session accounting to an enormous value. Repeated additions can eventually reach unusable numeric states in downstream calculations.

This is also inconsistent with the earlier audit ledger, which records shared ABS numeric validation as fixed. The audit11 transaction work moved this delta into the new `UpdateSessionWithProgress` helper but did not restore the full input policy at the handler boundary.

### Fix

Apply the same finite/nonnegative/policy-bound validation used for unknown-duration positions:

```go
if err := store.ValidPosition(body.TimeListened, 0); err != nil {
    fail(w, 400, "invalid timeListened")
    return
}
```

Do this in:

- `sessionSync`;
- `sessionClose`;
- any other ABS endpoint where `timeListened` is actually persisted.

For `postProgress`, `TimeListened` is currently parsed but not written to the constructed `store.Progress`; either remove the unused field if protocol compatibility permits, or validate it consistently if retaining it. Avoid the current pattern:

```go
if err := store.ValidPosition(body.TimeListened, 0); err != nil && body.TimeListened < 0
```

because it discards every policy failure except negativity.

For defense in depth, the store transaction helpers can reject invalid deltas too:

```go
func validateListenedDelta(v float64) error {
    return ValidPosition(v, 0)
}

func (d *DB) UpdateSessionWithProgress(..., listenedDelta float64) error {
    if err := validateListenedDelta(listenedDelta); err != nil {
        return err
    }
    ...
}
```

The handler should still map bad client input to 400; the store check protects future callers from bypassing the HTTP validation.

### Regression tests

For both sync and close:

- `timeListened=-1` -> 400;
- `timeListened=1e300` -> 400;
- `timeListened > 30 days` -> 400;
- a normal delta succeeds;
- rejected deltas do not mutate either session or progress;
- store-level helpers reject invalid deltas when called directly.

---

## Patch order

1. **A12-02 + A12-03 together** — both are small ABS numeric-integrity fixes in the same handlers and should share regression fixtures.
2. **A12-01** — first add the request-origin tests, then narrow cookie authority. If a scoped capability is considered too large for the immediate patch, ship method narrowing + same-origin validation first and keep the scoped ticket as the follow-on.

## Suggested consolidated ABS patch

A small helper keeps the two session handlers consistent:

```go
func validatedSessionTiming(ed *store.EditionView, current, clientDuration, listened float64) (float64, error) {
    duration := clientDuration
    if serverDuration := ed.TotalDuration(); serverDuration > 0 {
        duration = serverDuration
    }
    if err := store.ValidPosition(current, duration); err != nil {
        return 0, err
    }
    if err := store.ValidPosition(listened, 0); err != nil {
        return 0, fmt.Errorf("invalid timeListened")
    }
    return duration, nil
}
```

Then both sync and close:

```go
dur, err := validatedSessionTiming(ed, body.CurrentTime, body.Duration, body.TimeListened)
if err != nil {
    fail(w, 400, err.Error())
    return
}
```

This also makes it harder for a future transactional refactor to repeat the current validation-order bug.

## Validation checklist

After applying the fixes:

```sh
gofmt -w internal/api/abs internal/api/core internal/auth internal/store
go vet ./...
go test ./... -count=1 -timeout 600s
go test -race ./internal/auth ./internal/store ./internal/api/abs ./internal/api/core
```

Frontend:

```sh
cd web
npx tsc --noEmit
npm run test
npm run build
```

Targeted additions should specifically exercise:

- cookie-only media GET remains functional;
- cross-origin/same-site progress POST is rejected unless it passes the explicit mutation-origin policy;
- bearer-header progress writes remain functional;
- ABS sync/close use server-known edition duration as the validation bound;
- oversized `timeListened` never reaches SQLite;
- all rejected timing requests leave progress and playback-session rows unchanged.

## Notes on reviewed audit10/audit11 fixes

I rechecked the newly landed areas to avoid duplicating the ledger:

- audit11's `decodeBody(... allowEmpty ...)` rollout covers sync, ordinary ABS progress, and ABS podcast progress with the intended required/optional distinction;
- `UpdateSessionWithProgress` now makes the progress/session update failure-atomic;
- edition lookup errors in sync are surfaced rather than silently converted to success;
- KDF saturation is distinguished from entropy/internal errors in the new password-hash callers;
- importer temporary-password generation now propagates entropy errors;
- secure-cookie behavior is explicitly configurable rather than inferred from untrusted proxy headers.

Those are not re-reported here.

## Bottom line

audit10/audit11 closed the issues they targeted, but the new media-cookie boundary is still too permissive for a state-changing beacon route, and the ABS session handlers retain two numeric-integrity gaps that are easy to fix without reopening any standing architectural deferral.

The smallest safe next batch is therefore three contained changes: **same-origin/method containment for cookie-authenticated progress beacons, authoritative-duration validation for ABS session position, and bounded validation for `timeListened`.**