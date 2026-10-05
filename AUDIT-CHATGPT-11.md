# libteca audit follow-up after audit10

Date: 2026-10-04  
Audited revision: `a15c17f147448bf91de8a9c40ae515d9b021592c` (`main`, audit10 landing)  
Scope: read `AUDIT_OPEN.md` first; reviewed the audit10 delta plus adjacent ABS/auth/importer/session code. Standing deferrals and findings already marked fixed in `AUDIT_OPEN.md` are intentionally not re-reported.

## Executive summary

I found **5 actionable findings** that are not standing deferrals. Four are implementation/correctness bugs; one is a transport-hardening improvement introduced by the new media-cookie design.

| ID | Severity | Area | Finding |
|---|---|---|---|
| A11-01 | Medium | ABS | audit10's strict body decoder was not applied to `/session/{id}/sync` or progress mutation siblings |
| A11-02 | Medium | ABS/store | session sync updates playback session and progress non-atomically and silently ignores edition lookup failures |
| A11-03 | Medium | auth/core | new entropy failures from `HashRequest` are misreported as `429 KDF busy` by user creation/password reset |
| A11-04 | Medium | importer | temporary-password generation still panics on `crypto/rand` failure, bypassing audit10's entropy-error propagation |
| A11-05 | Low / hardening | auth/deployment | new bearer media cookie lacks a deployment-aware `Secure` option |

I explicitly did **not** reopen audit10's confirmed deferrals: multipart video, trickplay/media generations, physical-source identity, durable/offline media progress, transcode/trickplay byte budgets, Subsonic plaintext compatibility secret, or scanner identity.

---

## A11-01 — audit10 strict ABS body decoding is incomplete

**Severity:** Medium protocol correctness / consistency  
**Confidence:** Certain  
**Files:** `internal/api/abs/abs.go`, `internal/api/abs/body.go`

### Evidence

audit10 added `decodeBody` and applied it to `/items/{itemId}/play` and `/session/{id}/close`, including:

- a 1 MiB `MaxBytesReader` cap;
- 413 for `*http.MaxBytesError`;
- malformed JSON rejection;
- trailing-document rejection.

But `/session/{id}/sync` still does one unchecked-shape decode:

```go
r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
...
if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
    fail(w, 400, "Invalid body")
    return
}
```

So an oversized sync body is reported as 400 rather than 413, and a second JSON document is accepted. `postProgress` has the same old one-shot decoder. The audit10 report's own patch sketch explicitly said to use the shared decoder in `play`, `sessionSync`, `sessionClose`, progress mutations, and other ABS JSON handlers, so this is a missed implementation from that batch rather than a new architectural request.

There is one compatibility wrinkle: `decodeBody` currently treats an empty body as valid because `/play` and `/close` intentionally preserve that behavior. Historically `/sync` rejected EOF. Reusing the helper unchanged would accidentally convert an empty sync into a zero-valued mutation.

### Fix

Make required-vs-optional bodies explicit instead of overloading one helper:

```go
var (
    errTrailingJSON = errors.New("trailing json")
    errEmptyJSON    = errors.New("empty json body")
)

func decodeBody(w http.ResponseWriter, r *http.Request, max int64, allowEmpty bool, dst any) error {
    r.Body = http.MaxBytesReader(w, r.Body, max)
    dec := json.NewDecoder(r.Body)

    if err := dec.Decode(dst); err != nil {
        if errors.Is(err, io.EOF) {
            if allowEmpty {
                return nil
            }
            return errEmptyJSON
        }
        return err
    }

    var extra any
    if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
        if err == nil {
            return errTrailingJSON
        }
        return err
    }
    return nil
}
```

Call it as:

```go
// compatibility endpoints that intentionally accept an absent payload
if err := decodeBody(w, r, 1<<20, true, &body); err != nil { ... } // play/close

// existing required-body contract
if err := decodeBody(w, r, 1<<20, false, &body); err != nil { ... } // sync/progress
```

Apply the required form to at least:

- `sessionSync`;
- `postProgress`;
- ABS podcast episode progress POST (`internal/api/abs/podcasts.go`).

The login path may remain on its 32 KiB specialized decoder, or be moved to the same helper with `allowEmpty=false` while preserving its current messages/statuses.

### Regression tests

Add table-driven coverage asserting:

1. sync/progress malformed JSON -> 400 with no mutation;
2. sync/progress >1 MiB -> 413 with no mutation;
3. sync/progress two JSON documents -> 400 with no mutation;
4. empty sync remains 400;
5. empty play/close keeps the documented compatibility behavior.

---

## A11-02 — ABS session sync can partially commit and can turn an edition-read failure into success

**Severity:** Medium data consistency  
**Confidence:** Certain  
**Files:** `internal/api/abs/abs.go`, `internal/store/progress.go`

### Evidence

`sessionSync` currently performs these steps separately:

```go
if err := a.DB.UpdateSession(s.ID, body.CurrentTime, body.TimeListened); err != nil {
    ...
}
ed, err := a.DB.EditionByID(s.EditionID)
if err == nil {
    ...
    if err := a.DB.SetProgress(p); err != nil {
        ...
    }
}
write(w, 200, map[string]any{"success": true})
```

This has two independent correctness failures:

1. If `UpdateSession` succeeds and `SetProgress` fails, the request returns 500 but the playback-session row has already advanced. A retry can therefore add `timeListened` again while progress is still behind.
2. Any `EditionByID` error is silently ignored and the handler returns 200 after updating the session. An operational DB failure is therefore converted into an apparent successful sync with missing progress.

The repository already solved the analogous close path with `CloseSessionWithProgress`, which commits final progress and close in one transaction. This sync path never received the corresponding transactional helper.

### Fix

Add a transactional store operation for live sync, mirroring the invariants of `CloseSessionWithProgress`:

```go
func (d *DB) UpdateSessionWithProgress(s *Session, p *Progress, listenedDelta float64) error {
    return d.Update(func(tx *Tx) error {
        var owner, edition int64
        var closed sql.NullInt64
        err := tx.QueryRow(`
            SELECT user_id, edition_id, closed_at
            FROM playback_sessions
            WHERE id = ?`, s.ID).Scan(&owner, &edition, &closed)
        if errors.Is(err, sql.ErrNoRows) {
            return ErrNotFound
        }
        if err != nil {
            return err
        }
        if closed.Valid || owner != s.UserID || p.UserID != owner || p.EditionID != edition {
            return ErrNotFound
        }

        now := nowMilli()
        if err := setProgressAt(tx, p, now); err != nil {
            return err
        }

        res, err := tx.Exec(`
            UPDATE playback_sessions
            SET position_secs = ?,
                time_listened_secs = time_listened_secs + ?,
                updated_at = ?
            WHERE id = ? AND closed_at IS NULL`,
            p.EditionPositionSecs, listenedDelta, now, s.ID)
        if err != nil {
            return err
        }
        n, err := res.RowsAffected()
        if err != nil {
            return err
        }
        if n != 1 {
            return ErrNotFound
        }
        return nil
    })
}
```

Refactor the duplicated progress UPSERT used by `SetProgress`, `CloseSessionWithProgress`, and the new sync helper into an internal `setProgressAt(q, p, now)` helper so their semantics cannot drift.

In `sessionSync`, load the edition **before any mutation** and surface its error:

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

fileID, offset := ed.Locate(body.CurrentTime)
...
if err := a.DB.UpdateSessionWithProgress(s, p, body.TimeListened); err != nil {
    if errors.Is(err, store.ErrNotFound) {
        fail(w, 404, "Session not found")
    } else {
        serverError(w, r, err)
    }
    return
}
```

### Regression tests

Extend `internal/api/abs/progress_failure_test.go`:

- inject a failure on the progress UPSERT; assert session `position_secs` and `time_listened_secs` are unchanged;
- inject a failure on the playback-session UPDATE; assert progress is unchanged;
- force `EditionByID` to fail (e.g. failure trigger/view strategy or a store seam) and assert 500, not 200;
- repeat a failed sync and verify `time_listened` is not double-counted.

---

## A11-03 — `HashRequest` gained entropy errors, but callers still label every failure as KDF saturation

**Severity:** Medium error semantics / operability  
**Confidence:** Certain  
**Files:** `internal/auth/auth.go`, `internal/api/core/users.go`

### Evidence

audit10 correctly changed:

```go
func Hash(password string) (string, error)
```

and now `HashRequest` returns `Hash(password)`, so its error set includes secure-random-source failures in addition to `ErrKDFBusy` and context cancellation.

But both user creation and password rotation still do:

```go
hash, err := auth.HashRequest(r.Context(), body.Password)
if err != nil {
    auth.WriteRetryAfter(w, time.Second)
    writeJSON(w, http.StatusTooManyRequests,
        map[string]string{"error": "server busy, try again later"})
    return
}
```

That violates the standing pass-7 contract recorded in `AUDIT_OPEN.md`: “only real KDF capacity is 429.” An entropy failure is an internal server failure, not rate/capacity pressure; `Retry-After: 1` is misleading and can encourage pointless retries.

### Fix

Only map `ErrKDFBusy` to 429. Treat other failures as internal errors; request cancellation can simply return without manufacturing a retryable server-busy response.

A small helper keeps both endpoints consistent:

```go
func writeHashError(w http.ResponseWriter, r *http.Request, err error) {
    if errors.Is(err, auth.ErrKDFBusy) {
        auth.WriteRetryAfter(w, time.Second)
        writeJSON(w, http.StatusTooManyRequests,
            map[string]string{"error": "server busy, try again later"})
        return
    }
    if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
        return
    }
    slog.Error("libteca: password hash failed", "err", err)
    writeJSON(w, http.StatusInternalServerError,
        map[string]string{"error": "internal error"})
}
```

Use it at both `HashRequest` call sites.

An even cleaner API would wrap random-source failures in a named `auth.ErrEntropyUnavailable`, but callers do not need that distinction if 429 is reserved solely for `ErrKDFBusy`.

### Regression tests

The existing `auth.readRandom` test seam is package-private, so core tests cannot directly replace it. Either:

- add a narrow auth test-only seam around `HashRequest`, or
- expose a package-private function variable in core for `auth.HashRequest`.

Assert:

- `ErrKDFBusy` -> 429 + `Retry-After`;
- generic/entropy hash error -> 500 and no `Retry-After`;
- no user/password mutation occurs on either failure.

---

## A11-04 — importer temporary-password generation still panics on entropy failure

**Severity:** Medium availability / transactional robustness  
**Confidence:** Certain  
**File:** `internal/importer/importer.go`

### Evidence

audit10 changed importer password hashing to propagate `auth.Hash` errors:

```go
hash, err := auth.Hash(pw)
if err != nil {
    return nil, fmt.Errorf("create user %s: %w", u.Name, err)
}
```

But the password itself is still generated by:

```go
func tempPassword() string {
    b := make([]byte, 6)
    if _, err := rand.Read(b); err != nil {
        panic(err)
    }
    return hex.EncodeToString(b)
}
```

So the same entropy failure AUD-09 was intended to make recoverable can still crash the entire process during an ABS/Kavita import before `auth.Hash` is even reached. This is particularly undesirable because whole-import atomicity was deliberately implemented: a recoverable source-of-randomness error should roll back the import, not terminate the server process.

### Fix

Return the error and propagate it through `applyUsers`:

```go
var readRandom = rand.Read

func tempPassword() (string, error) {
    var b [16]byte
    if _, err := readRandom(b[:]); err != nil {
        return "", err
    }
    return hex.EncodeToString(b[:]), nil
}
```

Then:

```go
pw, err := tempPassword()
if err != nil {
    return nil, fmt.Errorf("create user %s temporary password: %w", u.Name, err)
}
hash, err := auth.Hash(pw)
if err != nil {
    return nil, fmt.Errorf("create user %s: %w", u.Name, err)
}
```

I recommend 16 random bytes rather than the current 6. The 12-hex-character password satisfies the length policy today, but imported temporary credentials can remain valid until an administrator rotates them; 128 random bits costs nothing meaningful and removes avoidable credential-strength ambiguity.

### Regression tests

Add an importer test that replaces the local `readRandom` seam with a forced error and asserts:

- no panic;
- import returns an error mentioning temporary-password generation;
- the destination snapshot is unchanged (reuse the whole-import snapshot helpers);
- retry after restoring randomness succeeds.

---

## A11-05 — media bearer cookie should support `Secure` when HTTPS is terminated upstream

**Severity:** Low / deployment hardening  
**Confidence:** Certain behavior; deployment-dependent impact  
**Files:** `internal/auth/auth.go`, `internal/server/server.go`, `cmd/libteca/main.go`

### Evidence

audit10 moved the first-party media credential from query strings to an HttpOnly cookie:

```go
http.SetCookie(w, &http.Cookie{
    Name:     MediaCookieName,
    Value:    value,
    Path:     "/api/core",
    HttpOnly: true,
    SameSite: http.SameSiteStrictMode,
})
```

There is no `Secure` attribute. libteca itself currently starts only `ListenAndServe`, and the README describes LAN/Tailscale as the intended deployment, so setting `Secure: true` unconditionally would break legitimate direct-HTTP installs. Conversely, a deployment behind HTTPS termination cannot currently ask libteca to emit a Secure cookie, because the backend sees plain HTTP and there is no trusted-proxy scheme configuration.

Do **not** infer this from `X-Forwarded-Proto` unconditionally: an untrusted direct client could spoof it.

### Fix

Add an explicit operator-controlled setting rather than unsafe proxy auto-detection, for example:

```text
--secure-cookie
LIBTECA_SECURE_COOKIE=true
```

Thread it into the core API/auth cookie setter:

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

Use the same `Secure` value when clearing the cookie so the deletion matches the cookie scope consistently across browsers.

If a general trusted-proxy configuration is introduced later, scheme inference can be added only for explicitly trusted proxy addresses.

### Tests/documentation

- cookie lifecycle test with secure mode on/off;
- clear-cookie response mirrors the attribute;
- README deployment note: enable secure cookies whenever the user-facing origin is HTTPS; leave off for direct HTTP LAN/Tailscale HTTP deployments.

---

# Recommended patch order

1. **A11-02** transactional ABS sync — highest data-integrity value.
2. **A11-01** finish the decoder rollout — small patch, closes an audit10 implementation miss.
3. **A11-03** correct hash error classification — small and directly caused by the audit10 API change.
4. **A11-04** remove the remaining importer entropy panic — small, preserves whole-import rollback guarantees.
5. **A11-05** add deployment-aware Secure-cookie configuration — hardening/configuration change.

# Suggested implementation details

## Share the progress UPSERT

`SetProgress` and `CloseSessionWithProgress` currently duplicate the same `INSERT ... ON CONFLICT ... revision = progress.revision + 1` statement. Before adding `UpdateSessionWithProgress`, factor that SQL into one helper accepting a `dbtx` and timestamp. That reduces the chance that live sync, close, and ordinary progress writes diverge on tombstone clearing or revision semantics.

Example shape:

```go
func setProgressAt(q dbtx, p *Progress, now int64) error {
    _, err := q.Exec(`INSERT INTO progress (...)
        VALUES (...)
        ON CONFLICT(user_id, edition_id) DO UPDATE SET
            ...,
            deleted = 0,
            revision = progress.revision + 1`, ...)
    return err
}
```

Then `setProgress`, `CloseSessionWithProgress`, and `UpdateSessionWithProgress` call the same helper.

## Keep body compatibility per endpoint

Do not globally make empty JSON legal or illegal. audit10 deliberately preserved empty `/play` and `/close`; `/sync` historically rejected it. Encode that contract at the call site (`allowEmpty`) and test it explicitly.

## Preserve audit10 deferral boundaries

None of these patches require:

- scoped/short-lived media tickets;
- multipart HLS/video generation work;
- source-identity migration;
- durable offline progress/CAS redesign;
- transcode/trickplay disk-budget policy;
- Subsonic secret migration;
- scanner identity migration.

They can therefore land as a narrow follow-up without reopening the standing architecture gates.

# Validation checklist

After implementing the above:

```sh
gofmt -w internal/api/abs internal/auth internal/api/core internal/importer internal/store cmd/libteca
go vet ./...
go test ./... -count=1 -timeout 600s
go test -race ./internal/auth ./internal/importer ./internal/store ./internal/api/abs ./internal/api/core
```

Also run the existing frontend gates because the media-cookie setting changes the login/me/logout contract surface even if no TypeScript change is required:

```sh
cd web
npm run test
npx tsc --noEmit
npm run build
```

Targeted regressions should specifically prove:

- failed ABS sync is failure-atomic;
- trailing/oversized sync and progress requests cannot mutate state;
- hash entropy failure is 500, while only KDF saturation is 429;
- importer RNG failure rolls back cleanly without panic;
- secure-cookie mode produces the expected cookie attributes.

# Audit boundary / non-findings

I reviewed `AUDIT_OPEN.md` before reporting and intentionally excluded items already fixed or standing-deferred. In particular, this report does **not** re-report audit10 AUD-01 through AUD-11 as findings. A11-01 is specifically the portion of audit10's proposed decoder rollout that did not actually land; A11-03/A11-04 are integration gaps created or exposed by AUD-09's new error-return contract.

This is a source audit and patch design, not a claim that the patches above were executed in CI or deployed. No repository writes were made.
