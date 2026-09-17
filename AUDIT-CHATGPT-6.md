# libteca repository audit

**Repository:** [libteca/libteca](https://github.com/libteca/libteca)  
**Audited revision:** [`47eb141fdd7565994b1671abd7679bcbdc324439`](https://github.com/libteca/libteca/commit/47eb141fdd7565994b1671abd7679bcbdc324439)  
**Revision date:** September 13, 2026  
**Report date:** September 17, 2026  
**Review method:** direct GitHub source inspection, cross-file reasoning, and limited isolated reproductions.

## Executive summary

This report consolidates **45 findings: 8 High, 26 Medium, and 11 Low**. Each finding includes its severity, evidence and impact, immutable source links, a suggested implementation, and regression checks. No Critical-severity exploit was established. Severity is a prioritization judgment, not a CVSS score or an assertion that each issue is remotely exploitable.

The highest-priority issues concern stale authentication after revocation, non-atomic password rotation, plaintext primary-password storage in the Subsonic adapter, unbounded concurrent password derivation, shutdown completion, unbounded podcast downloads, and service-worker caching of authenticated protocol responses. The report also covers backup integrity, database error propagation, podcast consistency and retries, transcode lifecycle/control, browser API behavior, EPUB restoration, and CI assurance.

**Scope and confidence.** This is the complete report of findings established in this review, not a guarantee that every repository defect has been found. The code was read at the pinned revision, not inferred from the README or copied from historical audit reports. Some findings are confirmed defects in particular source paths; others are explicitly labeled hardening recommendations or conditional problems in embedding, malformed-data, concurrency, or deployment scenarios. The coverage manifest near the end identifies reviewed files and important areas not comprehensively reviewed.

**Implementation status.** The code below is proposed remediation, not a merged patch or an integration-tested replacement repository. Full-function replacements and smaller insertion-point examples are identified in their sections. Shared changes have dependencies: do not paste all snippets independently or keep the original code alongside its replacement. Required caller migrations, imports, database migrations, compatibility decisions, and unfinished architectural work are stated where applicable. Regression checklists describe tests to add; they are not claims those tests were executed.

**Execution limits.** Isolated Python/SQLite and Node checks reproduced selected logic defects. The repository build, race suite, browser integration suite, and FFmpeg/client compatibility tests were not run. Local repository cloning failed because the execution environment could not resolve GitHub; direct GitHub connector reads remained available. The available Go toolchain was 1.23.2, while this revision's [`go.mod`](https://github.com/libteca/libteca/blob/47eb141fdd7565994b1671abd7679bcbdc324439/go.mod) requires 1.26.6. No dependency vulnerability scan or live exploitation of a deployed server was performed.

## Severity and deployment assumptions

| Severity | Interpretation in this report |
|---|---|
| High | A substantial credential, availability, or resource-integrity risk under the stated conditions; remediate before relying on the affected boundary. |
| Medium | A reproducible correctness, consistency, reliability, or authorization-control weakness with meaningful impact but more limited exposure or preconditions. |
| Low | A bounded edge case, conditional lifecycle problem, contract mismatch, or preventive assurance/hardening improvement. |

Authentication findings assume a network client can reach an enabled protocol face. Token-revocation races require a previously valid credential; database-at-rest findings require access to the database or a backup. Podcast-content findings require subscription to affected content. Browser cache findings apply to requests controlled by the web service worker, not native media clients. Conditional helper/embedding defects are distinguished from the normal CLI startup path.

## Finding index

| ID | Severity | Finding |
|---|---|---|
| [F01](#f01) | High | Bearer-token cache can resurrect revoked credentials |
| [F02](#f02) | High | OPDS Basic-auth cache has the same revocation race and crosses database boundaries |
| [F03](#f03) | High | Password rotation is non-atomic and races with token issuance |
| [F04](#f04) | High | Subsonic login persists the user’s primary password in plaintext |
| [F05](#f05) | High | Expensive password verification lacks a process-wide concurrency budget |
| [F06](#f06) | Medium | Argon2 verifier accepts malformed or excessive stored parameters |
| [F07](#f07) | Low | Unknown-user timing differs across authentication faces |
| [F08](#f08) | Medium | Bootstrap administration bypasses validation and is not atomic |
| [F09](#f09) | Medium | Bearer credentials are stored directly in the database with no built-in lifetime |
| [F10](#f10) | Low | Token last-seen metadata stops updating after its first cache miss |
| [F11](#f11) | High | Main returns before graceful shutdown and background cleanup finish |
| [F12](#f12) | Medium | HTTP request-body reads have no configured server deadline |
| [F13](#f13) | Low | CLI configuration precedence and validation are surprising |
| [F14](#f14) | Medium | Backup naming can collide and destroy the previous snapshot |
| [F15](#f15) | Medium | Cover backups are shared, overwritten, and not durably finalized |
| [F16](#f16) | Low | Retention can keep too many backups when the protected snapshot sorts oldest |
| [F17](#f17) | Medium | SQLite DSN construction misinterprets reserved characters in data paths |
| [F18](#f18) | Low | Database initialization leaks opened handles on later failures |
| [F19](#f19) | Medium | Read failures are silently represented as empty, missing, or successful data |
| [F20](#f20) | Medium | JSON serialization can fail after a successful HTTP status is committed |
| [F21](#f21) | Medium | Opening one work loads every work, edition, and file in its library |
| [F22](#f22) | Medium | Progress input validation and device attribution are incomplete |
| [F23](#f23) | Low | Invalid identifiers and library inputs produce misleading API responses |
| [F24](#f24) | Medium | File-serving and nullable metadata paths ignore error conditions |
| [F25](#f25) | Medium | Scan persistence errors are discarded, including in panic recovery |
| [F26](#f26) | Medium | Podcast validators are committed before the feed is successfully applied |
| [F27](#f27) | High | Podcast enclosures have no byte ceiling and can exhaust disk |
| [F28](#f28) | Medium | Episode filename collision fallback can overwrite a different episode |
| [F29](#f29) | Medium | Deleting a podcast library loses the file-ID set before deleting file rows |
| [F30](#f30) | Medium | RSS duration parsing accepts NaN, infinity, negatives, and malformed time fields |
| [F31](#f31) | Medium | Transcode capacity enforcement evicts active viewers |
| [F32](#f32) | Medium | A closed transcode manager can create new sessions without a reaper |
| [F33](#f33) | Medium | Hardware fallback treats successful short transcodes as failures |
| [F34](#f34) | Medium | Core HLS sessions are not tied to their issuing user or server-issued tickets |
| [F35](#f35) | Medium | HLS start positions and non-media editions are not validated strictly |
| [F36](#f36) | High | Service worker caches authenticated protocol endpoints outside its denylist |
| [F37](#f37) | Low | Service-worker activation deletes unrelated origin caches |
| [F38](#f38) | Medium | Browser API helper treats failed HTTP responses as successful data |
| [F39](#f39) | Low | Browser request-header merging and response types do not match the API |
| [F40](#f40) | Medium | EPUB percentage-only resume fails when location indexes are already cached |
| [F41](#f41) | Low | EPUB loading continues after unmount and carries stale state across editions |
| [F42](#f42) | Medium | Subsonic queue and metadata endpoints return success without implementing the operation |
| [F43](#f43) | Low | Checked-in CI does not enforce race testing or locked frontend installation |
| [F44](#f44) | Low | Repeated handler construction recreates destructive state and leaks backend ownership |
| [F45](#f45) | Medium | Unchanged podcast feeds prevent retrying failed episode downloads |

---

# Detailed findings

<a id="f01"></a>

## F01 — Bearer-token cache can resurrect revoked credentials

**Severity: High**  
**Evidence classification:** Confirmed source defect  
**Source at audited revision:** [`internal/auth/auth.go`](https://github.com/libteca/libteca/blob/47eb141fdd7565994b1671abd7679bcbdc324439/internal/auth/auth.go) · [`internal/api/core/users.go`](https://github.com/libteca/libteca/blob/47eb141fdd7565994b1671abd7679bcbdc324439/internal/api/core/users.go) · [`internal/store/users.go`](https://github.com/libteca/libteca/blob/47eb141fdd7565994b1671abd7679bcbdc324439/internal/store/users.go)

**Evidence and impact.** `UserForToken` reads the database and subsequently populates a process-global `sync.Map`. `tokenRevoke` updates the database and then deletes the cache entry. An authentication request can read an active token, pause, let revocation and invalidation complete, then populate the cache with the old result. Subsequent requests authenticate indefinitely because cache hits never consult `revoked_at` and have no expiration. This requires an already valid token and a concurrent revocation; it is not an unauthenticated token-forging vulnerability. The global cache also incorrectly shares authorization results across different `*store.DB` instances in one process.

**Suggested fix and implementation.** Remove the positive authorization cache. Keep the database authoritative on every new request. This also avoids caching password hashes and obsolete user records. Replace `UserForToken` with the following in `internal/auth/auth.go`; retain `InvalidateToken` temporarily as a no-op while its callers are removed. Remove the old `cache` variable and unused `sync` import.

```go
func UserForToken(db *store.DB, value string) (*store.User, bool) {
    if value == "" || len(value) > 256 {
        return nil, false
    }
    var u store.User
    err := db.QueryRow(`
        SELECT u.id, u.name, u.password_hash, u.is_admin,
               u.created_at, u.updated_at
        FROM tokens t JOIN users u ON u.id = t.user_id
        WHERE t.value = ? AND t.revoked_at IS NULL`, value).
        Scan(&u.ID, &u.Name, &u.PasswordHash, &u.IsAdmin,
             &u.CreatedAt, &u.UpdatedAt)
    if err != nil {
        return nil, false
    }
    return &u, true
}

// Transitional compatibility; authorization is no longer cached.
func InvalidateToken(string) {}
```

Apply the digest lookup change in F09 to this query when migrating token storage. Do not replace this fix with a TTL alone: that merely bounds, rather than removes, the revocation gap. A request that already passed authentication may finish; the guarantee is that a new request after revocation cannot reuse stale cached authorization.

**Regression checks.** Use a deterministic barrier between token lookup and cache publication to reproduce the original race; revoke, release the barrier, and verify the next request is rejected. Also authenticate the same token string against two separate test databases with different users and verify isolation.

<a id="f02"></a>

## F02 — OPDS Basic-auth cache has the same revocation race and crosses database boundaries

**Severity: High**  
**Evidence classification:** Confirmed source defect  
**Source at audited revision:** [`internal/api/opds/opds.go`](https://github.com/libteca/libteca/blob/47eb141fdd7565994b1671abd7679bcbdc324439/internal/api/opds/opds.go) · [`internal/api/core/users.go`](https://github.com/libteca/libteca/blob/47eb141fdd7565994b1671abd7679bcbdc324439/internal/api/core/users.go)

**Evidence and impact.** The package-global `basicCache` is keyed only by a digest of username/password, not by database or current password hash. `InvalidateUser` races with a verification already in progress. A successful old-password verification can insert a cache entry after the invalidation pass and remain usable for up to 15 minutes. Deletion has the same problem. Identical credentials in two servers embedded in one process can reuse the wrong numeric user ID.

**Suggested fix and implementation.** Query the current user on every request, but cache the expensive *verification result*, scoped to the API instance and the current stored hash. An old password hash must never select a current cache entry. This preserves OPDS browsing performance without treating the cache as the authorization authority.

Add these fields to `opds.API` and the following helper. The existing file already imports the packages used except `context` and `errors`, needed at the integration point for error handling as appropriate. `auth.VerifyRequest` is implemented in F05.

```go
// Fields added to API:
// basicMu sync.Mutex
// basicProofs map[[32]byte]time.Time

func (a *API) verifyBasic(r *http.Request, u *store.User, pass string) (bool, error) {
    key := sha256.Sum256([]byte(strconv.FormatInt(u.ID, 10) +
        "\x00" + u.PasswordHash + "\x00" + pass))
    now := time.Now()
    a.basicMu.Lock()
    until, hit := a.basicProofs[key]
    a.basicMu.Unlock()
    if hit && now.Before(until) {
        return true, nil
    }
    valid, err := auth.VerifyRequest(r.Context(), pass, u.PasswordHash)
    if err != nil || !valid {
        return false, err
    }
    a.basicMu.Lock()
    if a.basicProofs == nil {
        a.basicProofs = make(map[[32]byte]time.Time)
    }
    if len(a.basicProofs) >= 1024 {
        for k, expiry := range a.basicProofs {
            if !now.Before(expiry) {
                delete(a.basicProofs, k)
            }
        }
        if len(a.basicProofs) >= 1024 {
            // Safe performance fallback, never an authorization fallback.
            a.basicProofs = make(map[[32]byte]time.Time)
        }
    }
    a.basicProofs[key] = now.Add(15 * time.Minute)
    a.basicMu.Unlock()
    return true, nil
}
```

In `auth`, remove the old cache-hit branch entirely. Call `a.DB.UserByName(user)` **before** `verifyBasic` on every request, reject missing users, and handle `auth.ErrKDFBusy` as 429 rather than recording a wrong-password failure. Only pass `u.ID` to the handler after a successful result. Remove the old global cache; `InvalidateUser` can become a transitional no-op because the current database hash now gates every cache lookup. Combine with F07 for uniform missing-user verification.

**Regression checks.** Change a password while a verification is paused, then try the old password on a new request. Repeat for user deletion and for identical credentials in separate databases. Verify at most 1,024 proof entries are retained per API instance.

<a id="f03"></a>

## F03 — Password rotation is non-atomic and races with token issuance

**Severity: High**  
**Evidence classification:** Confirmed source defect  
**Source at audited revision:** [`internal/api/core/users.go`](https://github.com/libteca/libteca/blob/47eb141fdd7565994b1671abd7679bcbdc324439/internal/api/core/users.go) · [`internal/auth/auth.go`](https://github.com/libteca/libteca/blob/47eb141fdd7565994b1671abd7679bcbdc324439/internal/auth/auth.go) · [`internal/store/users.go`](https://github.com/libteca/libteca/blob/47eb141fdd7565994b1671abd7679bcbdc324439/internal/store/users.go) · [`internal/store/store.go`](https://github.com/libteca/libteca/blob/47eb141fdd7565994b1671abd7679bcbdc324439/internal/store/store.go)

**Evidence and impact.** `userSetPassword` separately updates the password, lists token values, revokes tokens, invalidates entries, and removes the Subsonic secret. An intermediate failure leaves a partially completed rotation. A login can also verify the old hash before rotation and issue a new token after the revocation statement, leaving an old-password-authenticated token active. Listing values before revocation additionally misses tokens created between the two operations when the old cache is retained.

**Suggested fix and implementation.** First apply F01/F02. Atomically change the hash and revoke existing tokens. Make password-authenticated issuance conditional on the exact hash that was verified. Make token-derived issuance conditional on an active parent token. Hash the new password before acquiring the database transaction.

Add to `internal/store/users.go`:

```go
func (d *DB) RotatePassword(id int64, passwordHash string) error {
    return d.Update(func(tx *Tx) error {
        res, err := tx.Exec(`UPDATE users SET password_hash=?, updated_at=?
                            WHERE id=?`, passwordHash, nowMilli(), id)
        if err != nil { return err }
        n, err := res.RowsAffected()
        if err != nil { return err }
        if n != 1 { return ErrNotFound }
        _, err = tx.Exec(`UPDATE tokens SET revoked_at=?
                         WHERE user_id=? AND revoked_at IS NULL`, nowMilli(), id)
        return err
    })
}
```

In `userSetPassword`, replace the separate update/list/revoke sequence with `a.DB.RotatePassword(id, auth.Hash(body.Password))`. Keep checked cleanup of the old Subsonic setting, and apply F04 to remove primary-password persistence altogether. The credential change and token invalidation must not depend on optional metadata cleanup succeeding.

Add to `internal/auth/auth.go`, using its existing `rand`, `hex`, `errors`, and `time` imports:

```go
var ErrCredentialsChanged = errors.New("credentials changed; authenticate again")

func newTokenValue() string {
    raw := make([]byte, 32)
    if _, err := rand.Read(raw); err != nil { panic(err) }
    return hex.EncodeToString(raw)
}

func IssueTokenForPassword(db *store.DB, userID int64, label, verifiedHash string) (string, error) {
    value := newTokenValue()
    res, err := db.Exec(`INSERT INTO tokens(user_id,label,value,created_at)
        SELECT id,?,?,? FROM users WHERE id=? AND password_hash=?`,
        label, value, time.Now().UnixMilli(), userID, verifiedHash)
    if err != nil { return "", err }
    n, err := res.RowsAffected()
    if err != nil { return "", err }
    if n != 1 { return "", ErrCredentialsChanged }
    return value, nil
}

func IssueTokenFromParent(db *store.DB, userID int64, label, parent string) (string, error) {
    value := newTokenValue()
    res, err := db.Exec(`INSERT INTO tokens(user_id,label,value,created_at)
        SELECT user_id,?,?,? FROM tokens
        WHERE value=? AND user_id=? AND revoked_at IS NULL`,
        label, value, time.Now().UnixMilli(), parent, userID)
    if err != nil { return "", err }
    n, err := res.RowsAffected()
    if err != nil { return "", err }
    if n != 1 { return "", ErrCredentialsChanged }
    return value, nil
}
```

Replace the core login issuance with `IssueTokenForPassword(a.DB, u.ID, r.UserAgent(), u.PasswordHash)`. Apply the same verified-hash condition to every other password-login issuer when integrating the patch. In `tokenIssue`, use `IssueTokenFromParent(a.DB, cur.ID, label, auth.Token(r))`. Map `ErrCredentialsChanged` to an authentication failure, not a successful response. Under F09, store `TokenKey(value)` and query `TokenKey(parent)` rather than raw values. Do not leave the old unconditional issuance path available to these handlers.

**Regression checks.** Pause a login after old-password verification, rotate the password, then resume issuance: issuance must fail. Exercise the reverse ordering: an issued token must be included in rotation's revocation. Inject a failure into the revocation update and verify the password update rolls back. Repeat for child-token issuance during parent revocation.

<a id="f04"></a>

## F04 — Subsonic login persists the user’s primary password in plaintext

**Severity: High**  
**Evidence classification:** Confirmed security exposure; compatibility trade-off  
**Source at audited revision:** [`internal/api/subsonic/subsonic.go`](https://github.com/libteca/libteca/blob/47eb141fdd7565994b1671abd7679bcbdc324439/internal/api/subsonic/subsonic.go) · [`internal/api/core/users.go`](https://github.com/libteca/libteca/blob/47eb141fdd7565994b1671abd7679bcbdc324439/internal/api/core/users.go)

**Evidence and impact.** A successful plain/hex Subsonic login writes the actual account password into `subsonic.pw.<userID>`. Subsequent `t`/`s` authentication uses that plaintext. This negates the database-at-rest protection of Argon2 for users who use this face; a database or backup disclosure exposes their primary password, not just a bearer token. This is explicit in the code, rather than a hypothetical dependency vulnerability. The legacy protocol's password-plus-salt MD5 requirement explains the implementation, but does not make retaining the primary password safe.

**Suggested fix and implementation.** The immediately implementable safe option is to disable legacy MD5-token authentication, retain plain/hex password authentication over an explicitly protected transport, and stop storing passwords. Report unsupported token authentication clearly rather than pretending the password is wrong.

In `wrap`, after parsing the form and before `authenticate`:

```go
if r.Form.Get("t") != "" {
    a.respond(w, r, errResponse(errNotImplemented,
        "Legacy MD5 token authentication is disabled; use a protected password connection"))
    return
}
```

Delete the entire `t` branch from `authenticate`, and delete the `GetSetting`/`SetSetting` password-capture block after successful plain-password verification. Add a one-time administrative migration using existing store methods:

```go
func removeLegacyPasswords(db *store.DB) error {
    users, err := db.Users()
    if err != nil { return err }
    for _, u := range users {
        if err := db.DeleteSetting(subsonicSecretKey(u.ID)); err != nil {
            return err
        }
    }
    return nil
}
```

Also remove orphaned `subsonic.pw.*` settings belonging to deleted users in the migration, using the actual settings-table schema. That migration must be tested against the repository's schema before release. Deleting current rows does not erase historical backups or necessarily erase old SQLite pages; rotate exposed credentials and retire sensitive historical backups according to the installation's retention policy.

If legacy `t`/`s` compatibility is a product requirement, use a **separate, revocable, Subsonic-only app secret**, with its own authorization scope and encrypted storage under a key outside the database/backup. Do not reuse the user's primary password. That is a deliberate protocol/credential migration, not a one-line hash substitution: hashing the primary password first does not preserve the protocol.

**Regression checks.** After a successful Subsonic password login, no primary-password setting may exist. Legacy token requests must receive an explicit unsupported response. Password rotation and user deletion must remove legacy material. Verify documentation and client compatibility after this intentional behavior change.

<a id="f05"></a>

## F05 — Expensive password verification lacks a process-wide concurrency budget

**Severity: High**  
**Evidence classification:** Confirmed resource-control gap  
**Source at audited revision:** [`internal/auth/auth.go`](https://github.com/libteca/libteca/blob/47eb141fdd7565994b1671abd7679bcbdc324439/internal/auth/auth.go) · [`internal/auth/limiter.go`](https://github.com/libteca/libteca/blob/47eb141fdd7565994b1671abd7679bcbdc324439/internal/auth/limiter.go) · [`internal/api/core/core.go`](https://github.com/libteca/libteca/blob/47eb141fdd7565994b1671abd7679bcbdc324439/internal/api/core/core.go) · [`internal/api/opds/opds.go`](https://github.com/libteca/libteca/blob/47eb141fdd7565994b1671abd7679bcbdc324439/internal/api/opds/opds.go) · [`internal/api/subsonic/subsonic.go`](https://github.com/libteca/libteca/blob/47eb141fdd7565994b1671abd7679bcbdc324439/internal/api/subsonic/subsonic.go)

**Evidence and impact.** Each `Hash`/`Verify` invocation uses an Argon2 memory parameter of 65,536 KiB. The limiter performs separate `Allow` and `Failure` operations and counts failures after verification. Many concurrent requests can all pass `Allow`; changing the username also changes the bucket, and core login deliberately performs a dummy hash verification for unknown usernames. The limiter therefore does not bound simultaneous memory-hard work. For example, 32 concurrently admitted 64-MiB derivations imply roughly 2 GiB of configured Argon2 working memory, before other process overhead. This is arithmetic from the code, not a measured load test.

**Suggested fix and implementation.** Add a fail-fast global KDF budget and bounded credential lengths. Do not hold a budget slot while streaming media, and do not create an unbounded waiting queue. Add to `internal/auth/auth.go` with a `context` import:

```go
var (
    ErrKDFBusy = errors.New("password verification capacity exhausted")
    kdfSlots = make(chan struct{}, 2)
)

func VerifyRequest(ctx context.Context, password, encoded string) (bool, error) {
    if len(password) > 1024 {
        return false, nil
    }
    select {
    case <-ctx.Done():
        return false, ctx.Err()
    default:
    }
    select {
    case kdfSlots <- struct{}{}:
        defer func() { <-kdfSlots }()
    default:
        return false, ErrKDFBusy
    }
    return Verify(password, encoded), nil
}
```

At each request-time password verification, call this helper and map `ErrKDFBusy` to 429 or the appropriate protocol error envelope, with `Retry-After: 1`; do not count capacity rejection as a failed password. Use the same budget for request-driven password creation/rotation around `Hash`. Keep a separate per-IP admission budget ahead of parsing/hashing in addition to the existing per-principal failure policy. Enforce bounded usernames and bodies; principal bucketing alone is not a resource budget.

**Regression checks.** Admit two blocked derivations and verify a third is rejected without starting Argon2. Confirm all login faces, unknown-user dummy verification, account creation, and password rotation share the budget. Check cancellation before admission and ensure streaming a previously authenticated OPDS page does not retain a slot.

<a id="f06"></a>

## F06 — Argon2 verifier accepts malformed or excessive stored parameters

**Severity: Medium**  
**Evidence classification:** Confirmed input-validation defect; requires malformed stored hash  
**Source at audited revision:** [`internal/auth/auth.go`](https://github.com/libteca/libteca/blob/47eb141fdd7565994b1671abd7679bcbdc324439/internal/auth/auth.go)

**Evidence and impact.** `Verify` does not validate `v=19`, parameter limits, the complete parameter string, or expected salt/hash lengths. Values parsed from a corrupted or imported hash can request excessive resources or make Argon2 panic, including zero iterations or parallelism. Normal remotely supplied passwords do not directly control the stored hash; this is database-corruption/import hardening, not an independently established remote exploit.

**Suggested fix and implementation.** Because the current writer emits one format, strictly accept that format initially. Replace `Verify` with:

```go
func Verify(password, encoded string) bool {
    if len(password) > 1024 || len(encoded) > 256 { return false }
    parts := strings.Split(encoded, "$")
    if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" ||
        parts[2] != "v=19" || parts[3] != "m=65536,t=2,p=1" {
        return false
    }
    salt, err := hex.DecodeString(parts[4])
    if err != nil || len(salt) != 16 { return false }
    want, err := hex.DecodeString(parts[5])
    if err != nil || len(want) != 32 { return false }
    got := argon2.IDKey([]byte(password), salt, 2, 64*1024, 1, 32)
    return subtle.ConstantTimeCompare(got, want) == 1
}
```

Remove the unused parameter-parsing code. If older installations contain other legitimate Argon2 parameter sets, add an explicit bounded allowlist and upgrade verified passwords on login; do not silently invalidate a documented older format.

**Regression checks.** Fuzz malformed separators, versions, zero/huge parameters, trailing parameter text, invalid hex, and wrong lengths. No input may panic or allocate according to attacker-selected parameters. Verify hashes generated by `Hash` still authenticate.

<a id="f07"></a>

## F07 — Unknown-user timing differs across authentication faces

**Severity: Low**  
**Evidence classification:** Confirmed information-disclosure hardening gap  
**Source at audited revision:** [`internal/api/core/core.go`](https://github.com/libteca/libteca/blob/47eb141fdd7565994b1671abd7679bcbdc324439/internal/api/core/core.go) · [`internal/api/opds/opds.go`](https://github.com/libteca/libteca/blob/47eb141fdd7565994b1671abd7679bcbdc324439/internal/api/opds/opds.go) · [`internal/api/subsonic/subsonic.go`](https://github.com/libteca/libteca/blob/47eb141fdd7565994b1671abd7679bcbdc324439/internal/api/subsonic/subsonic.go)

**Evidence and impact.** Core login uses a dummy Argon2 verification for missing users; OPDS short-circuits on `UserByName` error, and Subsonic returns before password verification for missing names. A caller can potentially distinguish unknown usernames from known usernames with wrong passwords. Network jitter and rate limits affect practical distinguishability; this review did not perform a timing attack.

**Suggested fix and implementation.** Centralize password checking, with uniform handling for a missing user and a wrong password. Apply F05 first so dummy work is bounded. Add a process-initialized dummy hash and this helper to `auth`:

```go
var dummyPasswordHash = Hash("libteca-non-account-dummy-password")

func CheckPassword(ctx context.Context, db *store.DB, name, password string) (*store.User, error) {
    u, lookupErr := db.UserByName(name)
    if lookupErr != nil && !errors.Is(lookupErr, store.ErrNotFound) {
        return nil, lookupErr
    }
    encoded := dummyPasswordHash
    if lookupErr == nil { encoded = u.PasswordHash }
    valid, err := VerifyRequest(ctx, password, encoded)
    if err != nil { return nil, err }
    if lookupErr != nil || !valid { return nil, ErrCredentialsChanged }
    return u, nil
}
```

Use equivalent response bodies and limiter accounting for invalid credentials. Preserve distinct internal logging for database outages; do not turn every database failure into a wrong-password lockout. The fixed dummy password is not an account credential and is never issued a token.

**Regression checks.** Verify the KDF is invoked for both missing users and wrong passwords, while database outages return a server error and do not increment the user's wrong-password count.

<a id="f08"></a>

## F08 — Bootstrap administration bypasses validation and is not atomic

**Severity: Medium**  
**Evidence classification:** Confirmed CLI correctness/security gap  
**Source at audited revision:** [`internal/auth/auth.go`](https://github.com/libteca/libteca/blob/47eb141fdd7565994b1671abd7679bcbdc324439/internal/auth/auth.go) · [`cmd/libteca/main.go`](https://github.com/libteca/libteca/blob/47eb141fdd7565994b1671abd7679bcbdc324439/cmd/libteca/main.go) · [`internal/api/core/users.go`](https://github.com/libteca/libteca/blob/47eb141fdd7565994b1671abd7679bcbdc324439/internal/api/core/users.go)

**Evidence and impact.** `InitAdmin` accepts empty names/passwords, unlike the web user-management password check. It separately checks whether users exist, inserts an admin, and inserts an undisclosed bootstrap token. A partial failure can create an account while reporting failure. An existing non-admin with the supplied password is accepted and the CLI prints that the admin is ready. Concurrent bootstrap processes can both observe an empty user table before insertion.

**Suggested fix and implementation.** Validate once, check the existing account's role, and put empty-install detection and insertion in an immediate transaction. Do not create an unused token that is never delivered. Replace `InitAdmin` as follows; add `database/sql` to its imports:

```go
func InitAdmin(db *store.DB, name, password string) error {
    name = strings.TrimSpace(name)
    if name == "" || len(name) > 128 || strings.ContainsRune(name, '\x00') {
        return fmt.Errorf("invalid admin name")
    }
    if len(password) < 8 || len(password) > 1024 {
        return fmt.Errorf("password must be 8 to 1024 bytes")
    }
    u, err := db.UserByName(name)
    if err == nil {
        if !u.IsAdmin { return fmt.Errorf("existing user is not an administrator") }
        if !Verify(password, u.PasswordHash) {
            return fmt.Errorf("existing administrator has a different password")
        }
        return nil
    }
    if !errors.Is(err, store.ErrNotFound) { return err }
    hash := Hash(password)
    return db.Update(func(tx *store.Tx) error {
        var count int
        if err := tx.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&count); err != nil {
            return err
        }
        if count != 0 { return fmt.Errorf("users already exist") }
        now := time.Now().UnixMilli()
        _, err := tx.Exec(`INSERT INTO users
            (name,password_hash,is_admin,created_at,updated_at) VALUES (?,?,1,?,?)`,
            name, hash, now, now)
        return err
    })
}
```

The replacement itself does **not** use `database/sql`; retain only imports actually used after applying it. The existing immediate transaction policy in `store.Open` supplies the required write serialization. Prefer a password file or interactive prompt over putting a secret in process arguments/shell history; document any bootstrap-interface change.

**Regression checks.** Empty/whitespace names, empty/short/oversized passwords, an existing non-admin, and concurrent bootstrap attempts must not produce a misleading success or multiple initial administrators. Inject an insert error and verify no account or token is left behind.

<a id="f09"></a>

## F09 — Bearer credentials are stored directly in the database with no built-in lifetime

**Severity: Medium**  
**Evidence classification:** Confirmed exposure; hardening recommendation  
**Source at audited revision:** [`internal/auth/auth.go`](https://github.com/libteca/libteca/blob/47eb141fdd7565994b1671abd7679bcbdc324439/internal/auth/auth.go) · [`internal/store/users.go`](https://github.com/libteca/libteca/blob/47eb141fdd7565994b1671abd7679bcbdc324439/internal/store/users.go)

**Evidence and impact.** `IssueToken` persists the usable bearer string directly in `tokens.value`; lookups check only `revoked_at`, not expiration. A stolen database/backup exposes active credentials. This is not a finding that tokens lack entropy: the random 32-byte generation is appropriate. Long-lived device tokens may be intentional, but that choice needs explicit lifetime/revocation and backup handling.

**Suggested fix and implementation.** Store a one-way digest, return the raw token only at issuance, and migrate existing rows before enabling digest-only lookup. This changes every token SQL reader/writer; apply it as one migration, not as an isolated helper replacement. Add to `auth` with `crypto/sha256`:

```go
func TokenKey(raw string) string {
    sum := sha256.Sum256([]byte(raw))
    return "sha256:" + hex.EncodeToString(sum[:])
}
```

Keep the existing column during the first migration to minimize schema churn: rewrite each existing `value` as `TokenKey(value)` inside an exclusive migration, skipping values already prefixed `sha256:`. Replace **all** authorization comparisons and parent-token predicates with `TokenKey(raw)` and all new token inserts with `TokenKey(value)`. The API still returns the raw `value`. Revocation by numeric token ID can operate without ever revealing a stored credential; F01 removes the need to return raw values for cache invalidation.

An explicit expiry policy can be added in a subsequent migration:

```sql
ALTER TABLE tokens ADD COLUMN expires_at INTEGER;
CREATE INDEX tokens_expiry ON tokens(expires_at);
```

Use `AND (t.expires_at IS NULL OR t.expires_at > ?)` in authorization, passing current Unix milliseconds. Set an expiry when issuing interactive browser sessions; reserve null expiry only for an explicitly chosen device/API-token policy. A migration cannot retroactively make old leaked backups safe: revoke credentials exposed by those backups.

**Regression checks.** Raw tokens must not appear in current database rows or API list responses. Migrated existing clients must keep working until the chosen expiry/revocation policy says otherwise. Validate F03's conditional issuance against the digested parent token. Expired tokens must fail even when previously used successfully.

<a id="f10"></a>

## F10 — Token last-seen metadata stops updating after its first cache miss

**Severity: Low**  
**Evidence classification:** Confirmed observability defect  
**Source at audited revision:** [`internal/auth/auth.go`](https://github.com/libteca/libteca/blob/47eb141fdd7565994b1671abd7679bcbdc324439/internal/auth/auth.go) · [`internal/api/core/users.go`](https://github.com/libteca/libteca/blob/47eb141fdd7565994b1671abd7679bcbdc324439/internal/api/core/users.go)

**Evidence and impact.** `last_seen_at` is updated only after a database lookup; a cache hit returns before the update. Frequently used tokens therefore look inactive for the rest of the process lifetime. Administrators cannot reliably use this field to identify stale devices. The update error is also ignored.

**Suggested fix and implementation.** With F01 removing the cache, throttle metadata writes in SQL rather than suppressing authorization checks. Add this best-effort update after successful authentication, importing `log/slog` if used:

```go
now := time.Now().UnixMilli()
if _, err := db.Exec(`UPDATE tokens SET last_seen_at=?
    WHERE value=? AND revoked_at IS NULL
      AND (last_seen_at IS NULL OR last_seen_at < ?)`,
    now, value, now-60_000); err != nil {
    slog.Warn("token activity update failed", "err", err)
}
```

Under F09, pass `TokenKey(value)` instead of `value`. Do not fail an otherwise valid media request merely because optional activity metadata could not be written, and never log token values. A higher-volume deployment can move this metadata update to a bounded, coalescing worker while keeping authorization synchronous.

**Regression checks.** A token used more than one minute after first authentication must get a later timestamp. A burst within the throttle window must not generate one write per request. Revoked tokens must not have activity refreshed.

<a id="f11"></a>

## F11 — Main returns before graceful shutdown and background cleanup finish

**Severity: High**  
**Evidence classification:** Confirmed lifecycle defect  
**Source at audited revision:** [`cmd/libteca/main.go`](https://github.com/libteca/libteca/blob/47eb141fdd7565994b1671abd7679bcbdc324439/cmd/libteca/main.go) · [`internal/server/server.go`](https://github.com/libteca/libteca/blob/47eb141fdd7565994b1671abd7679bcbdc324439/internal/server/server.go) · [`internal/api/core/core.go`](https://github.com/libteca/libteca/blob/47eb141fdd7565994b1671abd7679bcbdc324439/internal/api/core/core.go)

**Evidence and impact.** Shutdown runs in a goroutine, while the main goroutine returns as soon as `ListenAndServe` returns `http.ErrServerClosed`. That return occurs when shutdown closes the listener, not when active handlers finish. Deferred `db.Close` and process termination can therefore overtake `h.Shutdown`, `srv.Close`, transcode termination, websocket cleanup, and background database work. Podcast/watch goroutines are launched without a join. A stop signal can interrupt the work the graceful-shutdown path was supposed to protect.

**Suggested fix and implementation.** Own the shutdown sequence in the main goroutine. Run the HTTP server in a goroutine, not the cleanup. Give requests the server's root context, explicitly close upgraded connections/transcodes, and join owned workers before closing the database. The main-process splice is:

```go
// Add net and sync imports. Use the existing signal-derived ctx and stop.
var workers sync.WaitGroup
startWorker := func(fn func()) {
    workers.Add(1)
    go func() { defer workers.Done(); fn() }()
}
// Replace the existing untracked launches with startWorker calls:
// startWorker(func() { podcasts.Run(ctx) })
// startWorker(func() { watcher.Run(ctx) })

h.BaseContext = func(net.Listener) context.Context { return ctx }
serveErr := make(chan error, 1)
go func() { serveErr <- h.ListenAndServe() }()
var listenErr error
select {
case <-ctx.Done():
case listenErr = <-serveErr:
    stop()
}
stop()
shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
shutdownErr := h.Shutdown(shutdownCtx)
cancel()
if shutdownErr != nil {
    fmt.Fprintln(os.Stderr, "libteca: shutdown:", shutdownErr)
    _ = h.Close()
}
srv.Close()
workers.Wait()
srv.Core.WaitJobs() // Implement and register every API-owned worker below.
if listenErr != nil && !errors.Is(listenErr, http.ErrServerClosed) {
    fmt.Fprintln(os.Stderr, "libteca: serve:", listenErr)
}
// Only now may main return and the deferred database close execute.
```

For core-owned scan/import/metadata jobs, add a guarded job registry, rather than calling `WaitGroup.Add` concurrently with an unguarded shutdown `Wait`. Add `jobsMu sync.Mutex`, `jobsClosing bool`, and `jobsWG sync.WaitGroup` to `core.API`:

```go
func (a *API) launchJob(fn func()) bool {
    a.jobsMu.Lock()
    if a.jobsClosing {
        a.jobsMu.Unlock()
        return false
    }
    a.jobsWG.Add(1)
    a.jobsMu.Unlock()
    go func() { defer a.jobsWG.Done(); fn() }()
    return true
}

func (a *API) WaitJobs() {
    a.jobsMu.Lock()
    a.jobsClosing = true
    a.jobsMu.Unlock()
    a.jobsWG.Wait()
}
```

Replace each owned bare `go` launch with `launchJob`; reject new work with 503 if it returns false and clean up any job row already created. Those jobs must use the shutdown context and bounded external calls. The registration of all worker call sites is an integration requirement, not something the helper automatically accomplishes. Apply F32 so a closed transcode manager cannot restart work. Preserve a nonzero process exit code for an unexpected listen failure by returning an error from a `run()` function after cleanup; do not call `os.Exit` before deferred cleanup.

**Regression checks.** Hold an HTTP handler and a scan worker open, send SIGTERM, and verify the process waits for their cancellation/cleanup before database closure. Repeat with an occupied listen port, active websocket, and active transcode. Use subprocess tests, because an in-process handler test cannot prove main's exit ordering.

**External contract.** Go's [`http.Server.Shutdown` documentation](https://pkg.go.dev/net/http#Server.Shutdown) explicitly requires the program to wait for shutdown and notes that upgraded connections are not automatically handled.

<a id="f12"></a>

## F12 — HTTP request-body reads have no configured server deadline

**Severity: Medium**  
**Evidence classification:** Confirmed server configuration gap  
**Source at audited revision:** [`cmd/libteca/main.go`](https://github.com/libteca/libteca/blob/47eb141fdd7565994b1671abd7679bcbdc324439/cmd/libteca/main.go) · [`internal/server/server.go`](https://github.com/libteca/libteca/blob/47eb141fdd7565994b1671abd7679bcbdc324439/internal/server/server.go)

**Evidence and impact.** The HTTP server sets `ReadHeaderTimeout` and `IdleTimeout`, but not `ReadTimeout`. The former does not bound a body after headers have been read; the latter does not govern an active request. A client can occupy a handler with a slowly delivered body. The explicit core byte limit bounds size, not duration. This finding does not assume that all other faces lack framework-level size limits; their effective limits were not fully verified.

**Suggested fix and implementation.** Add a configurable body-read deadline. A conservative starting server configuration is:

```go
h := &http.Server{
    Addr:              fmt.Sprintf(":%d", *port),
    Handler:           srv.Handler(),
    ReadHeaderTimeout: 10 * time.Second,
    ReadTimeout:       30 * time.Second,
    IdleTimeout:       120 * time.Second,
    // Do not impose a short global WriteTimeout on long media/SSE responses.
}
```

Adjust the body window for documented large imports; alternatively use `http.ResponseController.SetReadDeadline` on those routes. Check `*http.MaxBytesError` in decoders and return 413 rather than treating every oversized body as generic malformed JSON. Keep both byte and time limits; neither replaces the other.

**Regression checks.** Send complete headers followed by an indefinitely stalled JSON body and verify bounded connection lifetime. Verify long GET-based media playback and SSE still work. Exercise the largest supported import on a slow but supported connection.

<a id="f13"></a>

## F13 — CLI configuration precedence and validation are surprising

**Severity: Low**  
**Evidence classification:** Confirmed configuration defects and deployment hardening  
**Source at audited revision:** [`cmd/libteca/main.go`](https://github.com/libteca/libteca/blob/47eb141fdd7565994b1671abd7679bcbdc324439/cmd/libteca/main.go) · [`internal/server/server.go`](https://github.com/libteca/libteca/blob/47eb141fdd7565994b1671abd7679bcbdc324439/internal/server/server.go) · [`README.md`](https://github.com/libteca/libteca/blob/47eb141fdd7565994b1671abd7679bcbdc324439/README.md)

**Evidence and impact.** `LIBTECA_WATCH` overwrites an explicitly supplied `--watch` flag after parsing. Invalid environment values are silently ignored. Sweep seconds are multiplied into a `time.Duration` without an overflow check. Invalid hardware-acceleration flags are logged as ignored and startup continues with another mode. The listen address is always all interfaces; this is documented, but operators cannot select a bind address independently of the port.

**Suggested fix and implementation.** Make explicit flags take precedence over environment defaults and fail clearly on invalid configuration. Insert after `flag.Parse`:

```go
watchWasSet := false
flag.Visit(func(f *flag.Flag) {
    if f.Name == "watch" { watchWasSet = true }
})
if !watchWasSet {
    if raw, ok := os.LookupEnv("LIBTECA_WATCH"); ok {
        v, err := strconv.ParseBool(raw)
        if err != nil { fatal(fmt.Errorf("LIBTECA_WATCH: %w", err)) }
        *watchEnabled = v
    }
}
if *port < 1 || *port > 65535 { fatal(fmt.Errorf("port must be 1..65535")) }

sweep := 6 * time.Hour
if raw, ok := os.LookupEnv("LIBTECA_SWEEP"); ok {
    v, err := strconv.ParseInt(raw, 10, 64)
    maxSeconds := int64(^uint64(0)>>1) / int64(time.Second)
    if err != nil || v < 0 || v > maxSeconds {
        fatal(fmt.Errorf("LIBTECA_SWEEP must be a nonnegative, representable number of seconds"))
    }
    sweep = time.Duration(v) * time.Second
}
```

Remove the old overriding environment block and duplicate sweep parsing. Validate hardware-acceleration choices before server startup instead of swallowing an explicitly requested invalid mode. Add `--host` and construct the address with `net.JoinHostPort`; preserving the old default `""` is backward-compatible, while a future loopback default should be announced as a behavior change. Plain HTTP on an untrusted network is not a protected password transport; document the reverse-proxy/Tailscale trust boundary rather than suggesting LAN membership encrypts credentials.

**Regression checks.** Test every explicit flag/environment combination, invalid booleans, negative/overflowing sweep values, and invalid ports. Confirm an explicit `--watch=false` cannot be undone by the environment.

<a id="f14"></a>

## F14 — Backup naming can collide and destroy the previous snapshot

**Severity: Medium**  
**Evidence classification:** Confirmed backup integrity defect  
**Source at audited revision:** [`internal/store/backup.go`](https://github.com/libteca/libteca/blob/47eb141fdd7565994b1671abd7679bcbdc324439/internal/store/backup.go) · [`cmd/libteca/main.go`](https://github.com/libteca/libteca/blob/47eb141fdd7565994b1671abd7679bcbdc324439/cmd/libteca/main.go)

**Evidence and impact.** Snapshot names have one-second resolution. `BackupTo` deletes any existing destination before running `VACUUM INTO`. Two snapshots in the same second, concurrent invocations, or a repeated local-clock timestamp can delete a completed snapshot; failure of the replacement then leaves no backup at that name. Fsyncing the replacement does not protect the prior file that was already deleted.

**Suggested fix and implementation.** Never remove an existing backup as preparation. Build in an exclusively created staging directory, fsync, and publish without replacement. Replace `BackupTo`:

```go
func (d *DB) BackupTo(dest string) error {
    if _, err := os.Lstat(dest); err == nil {
        return fmt.Errorf("backup destination already exists: %s", dest)
    } else if !os.IsNotExist(err) {
        return err
    }
    _, err := d.Exec(`VACUUM INTO ?`, dest)
    return err
}
```

Add `fmt` to the imports. Call it with an uncreated file inside a private staging directory. A backward-compatible `.db` publication helper, using existing `syncPath`, is:

```go
func (d *DB) publishDBBackup(dir string) (string, error) {
    if err := os.MkdirAll(dir, 0o700); err != nil { return "", err }
    stage, err := os.MkdirTemp(dir, ".libteca-stage-*")
    if err != nil { return "", err }
    defer os.RemoveAll(stage)
    tmp := filepath.Join(stage, "snapshot.db")
    if err := d.BackupTo(tmp); err != nil { return "", err }
    if err := syncPath(tmp); err != nil { return "", err }
    suffix := strings.TrimPrefix(filepath.Base(stage), ".libteca-stage-")
    name := "libteca-" + time.Now().UTC().Format("20060102T150405.000000000Z") +
        "-" + suffix + ".db"
    dest := filepath.Join(dir, name)
    // Same filesystem; Link publishes without overwriting an existing path.
    if err := os.Link(tmp, dest); err != nil { return "", err }
    if err := syncPath(dir); err != nil { return "", err }
    return dest, nil
}
```

In `Snapshot`, use this helper instead of the timestamp-only allocation/deleting `BackupTo` sequence, then perform covers/pruning. A filesystem that does not support hard links needs an equivalent no-replace publication primitive; do not fall back to deleting the old destination. F15 gives the preferable generation-directory format when changing the backup layout is acceptable.

**Regression checks.** Run multiple snapshots at the same frozen timestamp. Verify they produce distinct complete outputs. Pre-create the selected destination and inject `VACUUM` failure; its original contents must survive. Test interruption before publication and before pruning.

<a id="f15"></a>

## F15 — Cover backups are shared, overwritten, and not durably finalized

**Severity: Medium**  
**Evidence classification:** Confirmed backup completeness/durability gap  
**Source at audited revision:** [`internal/store/backup.go`](https://github.com/libteca/libteca/blob/47eb141fdd7565994b1671abd7679bcbdc324439/internal/store/backup.go)

**Evidence and impact.** All database snapshots share one `backups/covers` tree. Later backups overwrite artwork belonging to earlier database snapshots. `copyTree` truncates destination files in place, checks `io.Copy` but not output `Close`, and does not fsync cover files/directories. A crash can leave a successful-looking database backup with partial or missing cover copies. Source symlinks are followed by `os.Open`, despite `WalkDir` itself not following directory symlinks. The `defer` statements are inside the callback and are **not** a whole-tree file-descriptor leak.

**Suggested fix and implementation.** Store each database snapshot and its own cover tree in a staged generation, publish the generation only after all copies succeed, and prune complete generations. This requires updating restore documentation and pruning; it changes the current backup layout. Use checked, durable, non-symlink copying:

```go
func copyTreeDurable(src, dst string) error {
    if err := os.MkdirAll(dst, 0o700); err != nil { return err }
    var dirs []string
    err := filepath.WalkDir(src, func(p string, e os.DirEntry, walkErr error) error {
        if walkErr != nil { return walkErr }
        rel, err := filepath.Rel(src, p)
        if err != nil { return err }
        if rel == "thumb" && e.IsDir() { return filepath.SkipDir }
        outPath := filepath.Join(dst, rel)
        if e.IsDir() {
            if err := os.MkdirAll(outPath, 0o700); err != nil { return err }
            dirs = append(dirs, outPath)
            return nil
        }
        if !e.Type().IsRegular() {
            return fmt.Errorf("unsupported cover entry: %s", rel)
        }
        in, err := os.Open(p)
        if err != nil { return err }
        defer in.Close()
        out, err := os.OpenFile(outPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
        if err != nil { return err }
        _, err = io.Copy(out, in)
        if err == nil { err = out.Sync() }
        closeErr := out.Close()
        if err != nil { return err }
        return closeErr
    })
    if err != nil { return err }
    for i := len(dirs)-1; i >= 0; i-- {
        if err := syncPath(dirs[i]); err != nil { return err }
    }
    return nil
}

func (d *DB) SnapshotGeneration(coversDir, backupDir string) (string, error) {
    if err := os.MkdirAll(backupDir, 0o700); err != nil { return "", err }
    stage, err := os.MkdirTemp(backupDir, ".pending-*")
    if err != nil { return "", err }
    defer os.RemoveAll(stage)
    dbPath := filepath.Join(stage, "libteca.db")
    if err := d.BackupTo(dbPath); err != nil { return "", err }
    if err := syncPath(dbPath); err != nil { return "", err }
    if _, err := os.Stat(coversDir); err == nil {
        if err := copyTreeDurable(coversDir, filepath.Join(stage, "covers")); err != nil {
            return "", err
        }
    } else if !os.IsNotExist(err) { return "", err }
    if err := syncPath(stage); err != nil { return "", err }
    name := "libteca-" + time.Now().UTC().Format("20060102T150405.000000000Z") +
        "-" + strings.TrimPrefix(filepath.Base(stage), ".pending-")
    final := filepath.Join(backupDir, name)
    if _, err := os.Lstat(final); err == nil {
        return "", fmt.Errorf("snapshot generation already exists")
    } else if !os.IsNotExist(err) { return "", err }
    if err := os.Rename(stage, final); err != nil { return "", err }
    if err := syncPath(backupDir); err != nil { return "", err }
    return filepath.Join(final, "libteca.db"), nil
}
```

Use the returned generation as the restore unit and adapt F16 to prune only complete generation directories, never `.pending-*`. A generation does **not** by itself provide an atomic database-plus-files snapshot while cover writers mutate files. For that stronger guarantee, use immutable/content-addressed artwork referenced by the snapshot or a cross-process backup/mutation lock used by all artwork writers. The code above fixes publication, historical overwrite, and durability; it deliberately does not claim the stronger cross-resource guarantee. The directory must remain private to the service; checking a `DirEntry` alone is not a hostile-local-writer TOCTOU defense.

**Regression checks.** Overwrite artwork between two backups and verify both generations retain their own copies. Inject copy/sync/close errors and ensure no complete generation is published. Reject symlink/special-file cover entries. Restore both generations and test missing-cover behavior explicitly.

<a id="f16"></a>

## F16 — Retention can keep too many backups when the protected snapshot sorts oldest

**Severity: Low**  
**Evidence classification:** Confirmed retention edge case  
**Source at audited revision:** [`internal/store/backup.go`](https://github.com/libteca/libteca/blob/47eb141fdd7565994b1671abd7679bcbdc324439/internal/store/backup.go)

**Evidence and impact.** `pruneBackups` iterates only the first `len(dbs)-keep` names and skips `protect` within that fixed range. It does not delete a replacement candidate for the skipped entry. A clock-skewed protected snapshot can therefore leave more than `keep` backups. Local-time filenames also make lexical ordering ambiguous across clock changes.

**Suggested fix and implementation.** Count the protected file toward the retention budget and prune other files separately. Replace the deletion loop after collecting sorted `dbs`:

```go
var candidates []string
protectedPresent := false
for _, name := range dbs {
    p := filepath.Join(dir, name)
    if filepath.Clean(p) == filepath.Clean(protect) {
        protectedPresent = true
        continue
    }
    candidates = append(candidates, p)
}
slots := keep
if protectedPresent { slots-- }
removeCount := len(candidates) - slots
for i := 0; i < removeCount; i++ {
    if err := os.Remove(candidates[i]); err != nil { return err }
}
return nil
```

Keep the existing `keep < 1` no-prune guard. Use UTC identifiers and a recorded completion timestamp if chronology must survive manual clock changes. Under F15, apply the same budgeting to complete generation directories and sync the backup directory after pruning for durable directory-entry removal.

**Regression checks.** Protect the lexically oldest, middle, and newest backup with `keep=1` and larger values. Verify exactly the requested number survives when enough backups exist, and that the protected snapshot is never deleted.

<a id="f17"></a>

## F17 — SQLite DSN construction misinterprets reserved characters in data paths

**Severity: Medium**  
**Evidence classification:** Confirmed path-handling defect  
**Source at audited revision:** [`internal/store/store.go`](https://github.com/libteca/libteca/blob/47eb141fdd7565994b1671abd7679bcbdc324439/internal/store/store.go) · [`cmd/libteca/main.go`](https://github.com/libteca/libteca/blob/47eb141fdd7565994b1671abd7679bcbdc324439/cmd/libteca/main.go)

**Evidence and impact.** `store.Open` directly interpolates the filesystem path into a `file:` URI followed by query parameters. Paths containing `?`, `#`, or percent escapes can be interpreted as URI query/fragment/escape syntax rather than as the intended filename. A legitimate operator-selected data directory can therefore open the wrong path or fail unexpectedly. This is not remotely controlled SQL injection.

**Suggested fix and implementation.** Construct an escaped file URI and encode repeated pragma parameters. Add `net/url` and `path/filepath` imports and replace the DSN construction:

```go
abs, err := filepath.Abs(path)
if err != nil { return nil, err }
u := url.URL{Scheme: "file", Path: filepath.ToSlash(abs)}
q := url.Values{}
q.Set("_txlock", "immediate")
for _, pragma := range []string{
    "journal_mode(WAL)", "synchronous(NORMAL)",
    "busy_timeout(5000)", "foreign_keys(1)",
} {
    q.Add("_pragma", pragma)
}
u.RawQuery = q.Encode()
dsn := u.String()
```

Test against the actual `modernc.org/sqlite` version and supported Linux/macOS paths. This fixes URI construction without weakening the existing immediate transaction or foreign-key settings.

**Regression checks.** Open temporary data directories containing spaces, `?`, `#`, `%`, and Unicode. Verify the database is created at exactly the requested path and the expected pragmas remain active on new connections.

<a id="f18"></a>

## F18 — Database initialization leaks opened handles on later failures

**Severity: Low**  
**Evidence classification:** Confirmed resource-cleanup defect  
**Source at audited revision:** [`internal/store/store.go`](https://github.com/libteca/libteca/blob/47eb141fdd7565994b1671abd7679bcbdc324439/internal/store/store.go)

**Evidence and impact.** After `sql.Open`, failures from dialect setup, migrations, or search backfill return without closing `sdb`. Process exit may hide this in the CLI, but retries or embedded/test use can retain pools and file handles.

**Suggested fix and implementation.** Install failure cleanup immediately after opening and transfer ownership only on success:

```go
sdb, err := sql.Open("sqlite", dsn)
if err != nil { return nil, err }
success := false
defer func() {
    if !success { _ = sdb.Close() }
}()
// Keep the existing pool configuration, migrations, and backfill here.
// Immediately before the successful return:
success = true
return d, nil
```

Also avoid changing Goose's process-global filesystem/dialect configuration concurrently across database initializations; serialize setup or use its instance-scoped API when that refactor is undertaken. Concurrent Goose initialization was not dynamically tested, so that point is a conditional design risk, not an additional confirmed race finding.

**Regression checks.** Force migration and backfill failures, retry initialization, and verify the failed database handles are closed. Successful initialization must retain a usable pool.

<a id="f19"></a>

## F19 — Read failures are silently represented as empty, missing, or successful data

**Severity: Medium**  
**Evidence classification:** Confirmed error-handling defects  
**Source at audited revision:** [`internal/api/core/core.go`](https://github.com/libteca/libteca/blob/47eb141fdd7565994b1671abd7679bcbdc324439/internal/api/core/core.go) · [`internal/store/works.go`](https://github.com/libteca/libteca/blob/47eb141fdd7565994b1671abd7679bcbdc324439/internal/store/works.go) · [`internal/store/reading.go`](https://github.com/libteca/libteca/blob/47eb141fdd7565994b1671abd7679bcbdc324439/internal/store/reading.go)

**Evidence and impact.** `me` ignores `UserProgressList` errors; `getProgress` treats every progress-read failure as a zero-position successful response; work detail ignores several progress/page-count reads. `WorksInLibrary` does not check `erows.Err()` after iterating editions. `FileByID` returns `ErrNotFound` when `rows.Next()` is false without first checking the iteration error. `EditionByID` tests the empty file list before reporting `rows.Err()`. Database failures can become believable but false application state, potentially causing clients to overwrite valid progress.

**Suggested fix and implementation.** Distinguish absence from failure, and check every row iterator. For `getProgress`, check the edition exists and use:

```go
p, err := a.DB.GetReadingProgress(auth.UserID(r), eid)
if err != nil && !errors.Is(err, store.ErrNotFound) {
    writeJSON(w, 500, map[string]string{"error": "progress unavailable"})
    return
}
// Only store.ErrNotFound means a valid edition has no saved progress.
```

For `me` and work-detail queries, return a server error instead of silently substituting an empty collection. In `WorksInLibrary`, immediately after `erows.Close()`:

```go
if err := erows.Err(); err != nil { return nil, err }
```

In `FileByID`:

```go
if !rows.Next() {
    if err := rows.Err(); err != nil { return nil, err }
    return nil, ErrNotFound
}
```

In `EditionByID`, check `rows.Err()` before `len(ev.Files)==0`. Apply the same principle to `findWorkID`, which currently collapses every query failure into `false`; return `(int64, bool, error)` and propagate the error through its callers instead of interpreting database unavailability as absence.

**Regression checks.** Inject row-iteration errors after some rows, close the database before a progress request, and simulate a busy/erroring lookup. None may return a successful empty result or a misleading 404. A genuinely missing progress row must retain the intended default behavior.

<a id="f20"></a>

## F20 — JSON serialization can fail after a successful HTTP status is committed

**Severity: Medium**  
**Evidence classification:** Confirmed response-integrity defect  
**Source at audited revision:** [`internal/api/core/core.go`](https://github.com/libteca/libteca/blob/47eb141fdd7565994b1671abd7679bcbdc324439/internal/api/core/core.go) · [`internal/podcast/fetch.go`](https://github.com/libteca/libteca/blob/47eb141fdd7565994b1671abd7679bcbdc324439/internal/podcast/fetch.go)

**Evidence and impact.** `writeJSON` writes the status before encoding and ignores the encoder's error. Unsupported values, including non-finite floating-point values imported through other code paths, can yield a 200 response with an empty or incomplete body. JSON request decoding itself rejects literal NaN/Infinity; the relevant ingress example is feed parsing in F30, not ordinary JSON input.

**Suggested fix and implementation.** Serialize before writing headers and preserve the router's problem+json behavior for 404s. Replace `writeJSON`:

```go
func writeJSON(w http.ResponseWriter, status int, v any) {
    contentType := "application/json"
    if status == http.StatusNotFound {
        detail := "Not Found"
        if m, ok := v.(map[string]string); ok && m["error"] != "" {
            detail = m["error"]
        }
        contentType = "application/problem+json"
        v = map[string]any{
            "type": "https://neutron.dev/errors/not-found", "title": "Not Found",
            "status": status, "detail": detail,
        }
    }
    data, err := json.Marshal(v)
    if err != nil {
        slog.Error("JSON serialization failed", "status", status, "err", err)
        status = http.StatusInternalServerError
        contentType = "application/json"
        data = []byte(`{"error":"internal error"}`)
    }
    w.Header().Set("Content-Type", contentType)
    w.WriteHeader(status)
    if _, err := w.Write(append(data, '\n')); err != nil {
        slog.Debug("response write failed", "err", err)
    }
}
```

For streaming JSON, use an explicitly streaming contract rather than trying to rewind a committed status. F38 updates the browser to understand both ordinary error JSON and problem+json.

**Regression checks.** Pass `math.NaN()`, `math.Inf(1)`, and a valid response into the helper. Serialization failure must produce a valid 500 JSON response, while 404s must retain `application/problem+json` and their specific detail.

<a id="f21"></a>

## F21 — Opening one work loads every work, edition, and file in its library

**Severity: Medium**  
**Evidence classification:** Confirmed scalability defect  
**Source at audited revision:** [`internal/api/core/core.go`](https://github.com/libteca/libteca/blob/47eb141fdd7565994b1671abd7679bcbdc324439/internal/api/core/core.go) · [`internal/store/works.go`](https://github.com/libteca/libteca/blob/47eb141fdd7565994b1671abd7679bcbdc324439/internal/store/works.go)

**Evidence and impact.** `work` first fetches the requested work, then calls `WorksInLibrary` and searches its result for that single ID. That helper materializes all works, all editions, and all non-missing files for the library. Work detail also loads the user's complete progress collections. The cost of a single detail request therefore grows with unrelated library content, increasing memory, latency, and database pressure. This is visible from the query/data flow; no throughput benchmark was run.

**Suggested fix and implementation.** Add a work-scoped loader. This minimal implementation avoids the whole-library load; a subsequent optimization can batch the per-edition loads into two scoped queries:

```go
func (d *DB) WorkViewByID(id int64) (*WorkView, error) {
    w, err := d.WorkByID(id)
    if err != nil { return nil, err }
    rows, err := d.Query(`SELECT id FROM editions WHERE work_id=? ORDER BY id`, id)
    if err != nil { return nil, err }
    var ids []int64
    for rows.Next() {
        var eid int64
        if err := rows.Scan(&eid); err != nil { rows.Close(); return nil, err }
        ids = append(ids, eid)
    }
    rows.Close()
    if err := rows.Err(); err != nil { return nil, err }
    out := &WorkView{Work: *w, Editions: make([]EditionView, 0, len(ids))}
    for _, eid := range ids {
        ed, err := d.EditionByID(eid)
        if errors.Is(err, ErrNotFound) { continue }
        if err != nil { return nil, err }
        out.Editions = append(out.Editions, *ed)
    }
    return out, nil
}
```

Use this in `core.work` and remove the outer full-library loop. Load progress only for the returned edition IDs, using a work-scoped join or per-edition lookup; propagate errors as in F19. The minimal helper intentionally omits editions with no available files, matching `EditionByID`; if the UI must display unavailable editions, retain their metadata with an explicit availability flag instead of silently changing that behavior. Preserve ordering and response shape in contract tests.

**Regression checks.** Compare detail output before/after on a small fixture. Add tens of thousands of unrelated works and verify the number of loaded rows remains scoped to the requested work. Include a large TV work to justify the eventual batched edition/file implementation.

<a id="f22"></a>

## F22 — Progress input validation and device attribution are incomplete

**Severity: Medium**  
**Evidence classification:** Confirmed validation/metadata defects  
**Source at audited revision:** [`internal/api/core/core.go`](https://github.com/libteca/libteca/blob/47eb141fdd7565994b1671abd7679bcbdc324439/internal/api/core/core.go) · [`internal/store/reading.go`](https://github.com/libteca/libteca/blob/47eb141fdd7565994b1671abd7679bcbdc324439/internal/store/reading.go) · [`internal/store/types.go`](https://github.com/libteca/libteca/blob/47eb141fdd7565994b1671abd7679bcbdc324439/internal/store/types.go) · [`internal/store/works.go`](https://github.com/libteca/libteca/blob/47eb141fdd7565994b1671abd7679bcbdc324439/internal/store/works.go)

**Evidence and impact.** `setProgress` validates negative position and percentage bounds, but does not validate negative pages, negative duration, page count overflow, or locator/device length. It parses `Device` but never assigns it to `store.Progress.Device`. Database constraints, where present, would turn invalid input into a 500 rather than an appropriate client error. `Locate` permits an offset beyond the final file when a supplied position exceeds a known media duration. Games use cumulative playtime, so indiscriminately clamping every edition to duration would introduce a new bug.

**Suggested fix and implementation.** Validate the input at the API boundary and retain the parsed device. Add `math` to imports:

```go
if math.IsNaN(body.Position) || math.IsInf(body.Position, 0) || body.Position < 0 ||
    math.IsNaN(body.Duration) || math.IsInf(body.Duration, 0) || body.Duration < 0 {
    writeJSON(w, 400, map[string]string{"error": "invalid position or duration"})
    return
}
if body.Page != nil && *body.Page < 0 {
    writeJSON(w, 400, map[string]string{"error": "page must be nonnegative"})
    return
}
if len(body.Device) > 256 || (body.Locator != nil && len(*body.Locator) > 8192) {
    writeJSON(w, 400, map[string]string{"error": "progress metadata too large"})
    return
}
if !strings.HasPrefix(ed.Format, "game-") {
    if total := ed.TotalDuration(); total > 0 && body.Position > total {
        writeJSON(w, 400, map[string]string{"error": "position exceeds duration"})
        return
    }
}
// After constructing p:
if body.Device != "" { p.Device = &body.Device }
```

Check page bounds against the stored page count using the reader's documented zero-/one-based convention; do not choose a convention independently of the existing CBZ/PDF clients. Preserve a small explicit media-position tolerance if clients legitimately report slightly past the encoded endpoint. Consider a separate playtime field for games to avoid overloading seek-position semantics. Treat those contract choices as migrations, not hidden validation changes.

**Regression checks.** Negative pages/durations must return 400, not 500; supplied device labels must round-trip. Reject excessive locators. Test the exact final media position, a small tolerated overshoot if supported, and cumulative game playtime larger than zero-duration file metadata.

<a id="f23"></a>

## F23 — Invalid identifiers and library inputs produce misleading API responses

**Severity: Low**  
**Evidence classification:** Confirmed validation/contract gap  
**Source at audited revision:** [`internal/auth/auth.go`](https://github.com/libteca/libteca/blob/47eb141fdd7565994b1671abd7679bcbdc324439/internal/auth/auth.go) · [`internal/api/core/core.go`](https://github.com/libteca/libteca/blob/47eb141fdd7565994b1671abd7679bcbdc324439/internal/api/core/core.go) · [`internal/store/queries.go`](https://github.com/libteca/libteca/blob/47eb141fdd7565994b1671abd7679bcbdc324439/internal/store/queries.go)

**Evidence and impact.** `auth.Atoi64` discards parse errors and turns malformed/overflowing IDs into zero. `addLibrary` accepts whitespace names and arbitrary type strings, leaving invalid types to database error handling, which returns a generic 500. These are client-input failures, not necessarily server faults. This is not SQL injection: the values are parameterized.

**Suggested fix and implementation.** Parse external identifiers explicitly and validate library types before insertion. Add to core:

```go
func positiveID(w http.ResponseWriter, r *http.Request, field string) (int64, bool) {
    id, err := strconv.ParseInt(r.PathValue(field), 10, 64)
    if err != nil || id <= 0 {
        writeJSON(w, 400, map[string]string{"error": "invalid " + field})
        return 0, false
    }
    return id, true
}
```

Use it at external route boundaries instead of silently converting invalid text to zero. In `addLibrary`, after decoding:

```go
body.Name = strings.TrimSpace(body.Name)
if body.Name == "" || len(body.Name) > 200 || strings.ContainsRune(body.Path, '\x00') {
    writeJSON(w, 400, map[string]string{"error": "invalid name or path"})
    return
}
if body.Type == "" { body.Type = "audiobooks" }
switch body.Type {
case "movies", "tv", "music", "audiobooks", "books", "comics", "games":
    // Podcasts are created through the subscription service, not this path.
default:
    writeJSON(w, 400, map[string]string{"error": "unsupported library type"})
    return
}
```

Map known uniqueness conflicts to 409 and other database failures to 500. Preserve a documented distinction between malformed IDs (400) and well-formed absent IDs (404), rather than changing individual endpoints inconsistently.

**Regression checks.** Exercise nonnumeric, zero, negative, and overflowing IDs; whitespace names; unsupported library types; and duplicate libraries. Verify no invalid input is reported as a successful empty result or a generic server fault.

<a id="f24"></a>

## F24 — File-serving and nullable metadata paths ignore error conditions

**Severity: Medium**  
**Evidence classification:** Confirmed reliability defects; conditional filesystem hardening  
**Source at audited revision:** [`internal/api/core/core.go`](https://github.com/libteca/libteca/blob/47eb141fdd7565994b1671abd7679bcbdc324439/internal/api/core/core.go) · [`internal/api/core/hls.go`](https://github.com/libteca/libteca/blob/47eb141fdd7565994b1671abd7679bcbdc324439/internal/api/core/hls.go) · [`internal/store/types.go`](https://github.com/libteca/libteca/blob/47eb141fdd7565994b1671abd7679bcbdc324439/internal/store/types.go)

**Evidence and impact.** Core `serveFile` and HLS segment serving ignore `f.Stat()` errors and dereference the returned `FileInfo`. Work detail checks `Width` but then dereferences `Height` without independently checking it. I/O failures or inconsistent nullable metadata can panic a request. Serving a stored path also assumes the file has not been replaced with a directory/special file/symlink after scanning. No direct client-controlled path traversal was established; the latter requires local mutation of a scanned path or another trusted directory.

**Suggested fix and implementation.** Handle stat errors, reject non-regular files, and check nullable values independently. Replace the core helper:

```go
func serveFile(w http.ResponseWriter, r *http.Request, path string) {
    before, err := os.Stat(path)
    if err != nil || !before.Mode().IsRegular() {
        writeJSON(w, 404, map[string]string{"error": "file unavailable"})
        return
    }
    f, err := os.Open(path)
    if err != nil {
        writeJSON(w, 404, map[string]string{"error": "file unavailable"})
        return
    }
    defer f.Close()
    fi, err := f.Stat()
    if err != nil {
        writeJSON(w, 500, map[string]string{"error": "file stat failed"})
        return
    }
    if !fi.Mode().IsRegular() {
        writeJSON(w, 404, map[string]string{"error": "file unavailable"})
        return
    }
    http.ServeContent(w, r, filepath.Base(path), fi.ModTime(), f)
}
```

Use this helper for core HLS segments rather than duplicating unchecked stat logic. In work rendering:

```go
if f.Width != nil { file["width"] = *f.Width }
if f.Height != nil { file["height"] = *f.Height }
```

The pre-open check is an accidental-special-file guard, not a complete TOCTOU defense against hostile local writers. For that threat model, open relative to a trusted root using the supported Go root APIs and appropriate nonblocking/symlink policy, verify the opened descriptor, and keep the media directories outside untrusted write access. Do not claim `filepath.Clean` or a pre-open `Stat` alone prevents a symlink swap.

**Regression checks.** Test missing files, directories, a width-without-height record, and an injected stat failure. Exercise Range/HEAD behavior after refactoring. Separately test the chosen local-writer/symlink policy; it is not proven by ordinary HTTP path-traversal tests.

<a id="f25"></a>

## F25 — Scan persistence errors are discarded, including in panic recovery

**Severity: Medium**  
**Evidence classification:** Confirmed job-state reliability defect  
**Source at audited revision:** [`internal/api/core/core.go`](https://github.com/libteca/libteca/blob/47eb141fdd7565994b1671abd7679bcbdc324439/internal/api/core/core.go)

**Evidence and impact.** The periodic scan callback ignores `UpdateScanJobCounts` errors. The panic-recovery path ignores both counts and `FinishScanJob` errors, then marks the in-memory run finished. A failed database write can leave the durable job running while memory says error/finished. The admission and watcher paths consult persisted running jobs, so this can create stuck-looking jobs until recovery/restart. The source evidence here is the visible periodic and panic paths, not a claim about every unseen scanner implementation.

**Suggested fix and implementation.** Check persistence errors, retry terminal writes with bounded backoff, and never present a successfully persisted terminal state unless the terminal write succeeded. Add to core:

```go
func (a *API) persistScanTerminal(run *scanRun, status string, message *string) error {
    p := run.snapshot()
    if err := a.DB.UpdateScanJobCounts(run.jobID,
        int64(p.FilesSeen), int64(p.FilesProbed), int64(p.FilesAdded),
        int64(p.FilesUpdated), int64(p.WorksChanged)); err != nil {
        slog.Warn("scan counts persistence failed", "job", run.jobID, "err", err)
    }
    var lastErr error
    for attempt := 0; attempt < 3; attempt++ {
        if err := a.DB.FinishScanJob(run.jobID, status, message); err == nil {
            return nil
        } else { lastErr = err }
        if attempt < 2 { time.Sleep(time.Duration(attempt+1) * 100 * time.Millisecond) }
    }
    return fmt.Errorf("persist terminal scan state: %w", lastErr)
}
```

Call it from panic recovery and other terminal paths; log and surface finalization failure instead of discarding it. Retain a failed-finalization record in a bounded retry/reconciliation queue so a recovered database can be updated without restarting. A durable outbox or startup reconciliation is required if terminal intent must survive process death; the retry helper alone cannot guarantee durability during a persistent database outage. F11 ensures cleanup does not close the database while finalization is still running.

**Regression checks.** Fail the first terminal update and verify retry succeeds. Fail every update and verify the UI/logs report finalization failure rather than a false durable success, and the reconciliation path eventually clears the running row after database recovery. Reproduce the same cases after a scanner panic.

<a id="f26"></a>

## F26 — Podcast validators are committed before the feed is successfully applied

**Severity: Medium**  
**Evidence classification:** Confirmed consistency defect  
**Source at audited revision:** [`internal/podcast/podcast.go`](https://github.com/libteca/libteca/blob/47eb141fdd7565994b1671abd7679bcbdc324439/internal/podcast/podcast.go) · [`internal/podcast/fetch.go`](https://github.com/libteca/libteca/blob/47eb141fdd7565994b1671abd7679bcbdc324439/internal/podcast/fetch.go)

**Evidence and impact.** Both subscription and refresh save the new ETag/Last-Modified before applying episodes. Several metadata update errors are ignored. If an episode upsert or later required step fails, the next conditional fetch can receive 304 and skip the unapplied data. This can leave a partially imported feed looking current until the upstream feed changes.

**Suggested fix and implementation.** Treat HTTP validators as a commit marker for successful feed-metadata/episode ingestion. Advance them only after checked application succeeds. In `refresh`, replace the early update and changed-feed block with:

```go
if !changed {
    if err := s.DB.UpdatePodcastFetch(p.ID, p.ETag, p.LastModified, nowMs()); err != nil {
        return p, false, err
    }
    return p, false, nil // F45 adds independent pending-download retry here.
}
if feed.Title != "" {
    if err := s.DB.UpdatePodcastMeta(p.ID, feed.Title,
        strPtr(feed.Author), strPtr(feed.Description)); err != nil {
        return p, true, err
    }
    p.Title = feed.Title
}
if err := s.applyFeed(ctx, p, feed); err != nil {
    return p, true, err
}
if err := s.DB.UpdatePodcastFetch(p.ID, nilOrEmpty(newETag), nilOrEmpty(newMod), nowMs()); err != nil {
    return p, true, err
}
fresh, err := s.DB.Podcast(p.ID)
return fresh, true, err
```

Retain the existing cover-fetch branch at the appropriate point; an optional cover failure should create retriable asset work, not silently discard the only opportunity to fetch it. In `Subscribe`, likewise move the validator write after successful `applyFeed` and check the result. The strongest implementation commits metadata, episode upserts, and validators in one database transaction and schedules downloads separately; repeated idempotent upserts are a safe intermediate step. Do not advance the validator simply because the HTTP fetch itself succeeded.

**Regression checks.** Serve ETag A, inject a failure after the first episode, and verify the database does not record A as successfully applied. The next refresh must still ingest the missing episodes even if the upstream feed has not changed. Test metadata-write and validator-write failures separately.

<a id="f27"></a>

## F27 — Podcast enclosures have no byte ceiling and can exhaust disk

**Severity: High**  
**Evidence classification:** Confirmed resource-exhaustion risk from subscribed content  
**Source at audited revision:** [`internal/podcast/download.go`](https://github.com/libteca/libteca/blob/47eb141fdd7565994b1671abd7679bcbdc324439/internal/podcast/download.go) · [`internal/podcast/podcast.go`](https://github.com/libteca/libteca/blob/47eb141fdd7565994b1671abd7679bcbdc324439/internal/podcast/podcast.go)

**Evidence and impact.** `downloadEpisode` copies an enclosure to disk with unbounded `io.Copy`. The size-derived deadline has a floor but no practical upper ceiling for a large declared Content-Length. A malicious or broken subscribed feed can stream arbitrarily large content or sustain a very long transfer. The two-worker pool and retention count do not bound bytes per episode or aggregate disk use. Exploitation requires a subscription or enclosure URL controlled by the attacker, not arbitrary unauthenticated direct download access.

**Suggested fix and implementation.** Use a configurable per-episode byte cap, a maximum elapsed download time, and an aggregate storage budget. The following is an example 2-GiB/2-hour per-episode policy; choose documented installation defaults rather than hiding arbitrary limits.

Add to `download.go`:

```go
const maxEpisodeBytes int64 = 2 << 30
const maxEpisodeReadTime = 2 * time.Hour

func (s *Service) downloadTimeout(contentLength int64) time.Duration {
    floor := s.dlReadFloor
    if floor <= 0 { floor = 90 * time.Second }
    seconds := contentLength / (32 * 1024)
    maxSeconds := int64(maxEpisodeReadTime / time.Second)
    if seconds > maxSeconds { return maxEpisodeReadTime }
    if seconds < 0 { seconds = 0 }
    duration := time.Duration(seconds) * time.Second
    if duration < floor { duration = floor }
    if duration > maxEpisodeReadTime { duration = maxEpisodeReadTime }
    return duration
}
```

After checking the HTTP status, reject a declared length above the byte cap. Replace the copy with:

```go
if resp.ContentLength > maxEpisodeBytes {
    return fmt.Errorf("episode exceeds configured size limit")
}
// Immediately after CreateTemp:
defer os.Remove(tmpPath) // Safe after a successful rename too.
size, err := io.Copy(io.MultiWriter(tmp, h),
    io.LimitReader(resp.Body, maxEpisodeBytes+1))
if err == nil && size > maxEpisodeBytes {
    err = fmt.Errorf("episode exceeds configured size limit")
}
if err == nil && size == 0 { err = fmt.Errorf("empty episode") }
if closeErr := tmp.Close(); err == nil { err = closeErr }
if err != nil { return err }
```

Reserve aggregate storage capacity before admission, including in-flight temporary files, and release the reservation on failure. A per-file cap alone does not prevent many legitimate-sized episodes from filling a small disk. The aggregate budget needs installation-specific policy and a shared reservation mechanism; it was not implemented or load-tested in this review.

**Regression checks.** Test chunked responses without Content-Length, lying/small Content-Length handled by the HTTP client, oversized declared lengths, stalled bodies, empty bodies, cancellation, and a full disk. Verify `.part` cleanup and maximum simultaneously reserved bytes.

<a id="f28"></a>

## F28 — Episode filename collision fallback can overwrite a different episode

**Severity: Medium**  
**Evidence classification:** Confirmed data-integrity defect; isolated reproduction executed  
**Source at audited revision:** [`internal/podcast/download.go`](https://github.com/libteca/libteca/blob/47eb141fdd7565994b1671abd7679bcbdc324439/internal/podcast/download.go)

**Evidence and impact.** A conflicting title path is changed once to `<title>-<episodeID>.<ext>`, without checking whether that fallback is already another episode's original title path. For example, existing `A.mp3` and `A-3.mp3` belong to episodes 1 and 2; episode 3 titled `A` chooses `A-3.mp3` and `os.Rename` overwrites episode 2's file. This does not require concurrent requests. An isolated Python reproduction of the exact naming decision selected the already-owned filename.

**Suggested fix and implementation.** Use an always-present immutable episode ID as the filename identity, not a title that can imitate the fallback. Replace the title/collision selection with:

```go
if ep.ID <= 0 { return fmt.Errorf("episode must be persisted before download") }
ext := enclosureExt(ep.EnclosureURL)
path := filepath.Join(dir, strconv.FormatInt(ep.ID, 10)+"."+ext)
```

Titles remain metadata, not filesystem identity. Existing downloaded files can continue using their stored paths; migrate them transactionally or switch only on redownload. If the enclosure extension changes for the same episode, remove the obsolete file only after the new file and database link are successfully committed. Never remove the previous good copy before a replacement has been validated.

**Regression checks.** Use the `A`/`A-3` fixture, repeated titles, non-ASCII titles that sanitize identically, and enclosure-extension changes. A download must never overwrite a path belonging to another episode, regardless of title text.

<a id="f29"></a>

## F29 — Deleting a podcast library loses the file-ID set before deleting file rows

**Severity: Medium**  
**Evidence classification:** Confirmed database-cleanup defect; isolated SQL reproduction executed  
**Source at audited revision:** [`internal/store/queries.go`](https://github.com/libteca/libteca/blob/47eb141fdd7565994b1671abd7679bcbdc324439/internal/store/queries.go) · [`internal/podcast/download.go`](https://github.com/libteca/libteca/blob/47eb141fdd7565994b1671abd7679bcbdc324439/internal/podcast/download.go)

**Evidence and impact.** In the podcasts branch of `DeleteLibrary`, episode rows are deleted first, then file IDs are selected from those now-deleted episode rows. The second statement therefore selects nothing. Podcast files use `edition_id NULL`, so the earlier edition-based file deletion does not catch them. This leaves orphan file rows. The higher-level service may remove disk files separately; this finding specifically establishes the store-level row leak, not an independently verified disk-retention or unauthorized-download exploit.

**Suggested fix and implementation.** Capture the IDs in the same immediate transaction before deleting episode rows, then delete only now-unreferenced podcast file rows. Replace the two statements in the podcast branch with:

```go
rows, err := tx.Query(`SELECT DISTINCT file_id FROM podcast_episodes
    WHERE podcast_id IN (SELECT id FROM podcasts WHERE library_id=?)
      AND file_id IS NOT NULL`, id)
if err != nil { return err }
var fileIDs []int64
for rows.Next() {
    var fid int64
    if err := rows.Scan(&fid); err != nil { rows.Close(); return err }
    fileIDs = append(fileIDs, fid)
}
rows.Close()
if err := rows.Err(); err != nil { return err }
if _, err := tx.Exec(`DELETE FROM podcast_episodes
    WHERE podcast_id IN (SELECT id FROM podcasts WHERE library_id=?)`, id); err != nil {
    return err
}
for _, fid := range fileIDs {
    if _, err := tx.Exec(`DELETE FROM files WHERE id=? AND edition_id IS NULL
        AND NOT EXISTS (SELECT 1 FROM podcast_episodes WHERE file_id=files.id)`, fid); err != nil {
        return err
    }
}
```

Keep the existing progress deletion and later podcast/library deletion inside the same transaction. Repair historical orphan rows only during a maintenance window with download/linking workers stopped, or after making file insertion/linking atomic; otherwise a cleanup query could delete an in-flight file row not yet linked to its episode. Disk deletion must retain the existing path-root safety checks.

**Regression checks.** The isolated SQLite fixture left one file row under the original delete ordering and zero with the snapshot-ID fix. Add the equivalent repository-level test with a downloaded episode, foreign keys enabled, and another unrelated podcast file that must survive.

<a id="f30"></a>

## F30 — RSS duration parsing accepts NaN, infinity, negatives, and malformed time fields

**Severity: Medium**  
**Evidence classification:** Confirmed parser validation defect  
**Source at audited revision:** [`internal/podcast/fetch.go`](https://github.com/libteca/libteca/blob/47eb141fdd7565994b1671abd7679bcbdc324439/internal/podcast/fetch.go) · [`internal/podcast/podcast.go`](https://github.com/libteca/libteca/blob/47eb141fdd7565994b1671abd7679bcbdc324439/internal/podcast/podcast.go)

**Evidence and impact.** `parseDurationSecs` returns `strconv.ParseFloat` results without finite/nonnegative checks and accepts any number of colon-separated components. Strings such as `NaN`, `+Inf`, `-30`, or overflowing accumulated components are not rejected consistently. These values feed episode metadata. The exact behavior of the SQLite driver for every non-finite value was not executed; the confirmed defect is acceptance at the parser boundary, with downstream JSON/progress reliability risk.

**Suggested fix and implementation.** Replace the parser with a finite, bounded, explicit one-to-three-field implementation; add `math` to imports:

```go
func parseDurationSecs(s string) float64 {
    s = strings.TrimSpace(s)
    if s == "" { return 0 }
    parts := strings.Split(s, ":")
    if len(parts) > 3 { return 0 }
    const maxDuration = 30 * 24 * 60 * 60 // Example documented policy: 30 days.
    total := 0.0
    for i, part := range parts {
        v, err := strconv.ParseFloat(strings.TrimSpace(part), 64)
        if err != nil || math.IsNaN(v) || math.IsInf(v, 0) || v < 0 {
            return 0
        }
        if len(parts) > 1 && i > 0 && v >= 60 { return 0 }
        total = total*60 + v
        if math.IsNaN(total) || math.IsInf(total, 0) || total > maxDuration {
            return 0
        }
    }
    return total
}
```

A zero result preserves the existing unknown/unparseable convention. Validate negative enclosure lengths as unknown/rejected metadata too. Keep literal protocol support explicit rather than accepting arbitrary float syntax accidentally. F20 remains necessary as a defense at the serialization boundary.

**Regression checks.** Table-test normal seconds, MM:SS, HH:MM:SS, fractions, blanks, NaN/Inf, negatives, more than three components, out-of-range minute/second fields, and overflow. Fuzzing must never emit a non-finite or negative duration.

<a id="f31"></a>

## F31 — Transcode capacity enforcement evicts active viewers

**Severity: Medium**  
**Evidence classification:** Confirmed availability/fairness defect  
**Source at audited revision:** [`internal/transcode/transcode.go`](https://github.com/libteca/libteca/blob/47eb141fdd7565994b1671abd7679bcbdc324439/internal/transcode/transcode.go) · [`internal/api/core/hls.go`](https://github.com/libteca/libteca/blob/47eb141fdd7565994b1671abd7679bcbdc324439/internal/api/core/hls.go)

**Evidence and impact.** When `MaxSessions` is reached, `Manager.Get` kills the least-recently-hit session even if it is still active. The ninth playback request can interrupt an existing viewer; repeated requests can churn the whole pool. The implementation does have a session cap of eight, so an assertion of unlimited concurrent FFmpeg sessions would be incorrect.

**Suggested fix and implementation.** Reclaim only genuinely idle sessions and reject excess admission rather than evicting active playback. Add `errors` to the transcode imports:

```go
var ErrCapacity = errors.New("transcode capacity exhausted")
```

Replace the `for len(m.sessions) >= MaxSessions` eviction loop, while retaining the manager lock:

```go
now := time.Now()
for id, session := range m.sessions {
    if now.Sub(time.Unix(0, session.lastHit.Load())) > idleSessionTTL {
        session.kill()
        delete(m.sessions, id)
    }
}
if len(m.sessions) >= MaxSessions {
    return nil, ErrCapacity
}
```

In core `hlsFile`, distinguish capacity from an internal transcode failure:

```go
if errors.Is(err, transcode.ErrCapacity) {
    w.Header().Set("Retry-After", "5")
    writeJSON(w, 503, map[string]string{"error": "transcode capacity exhausted"})
    return
}
```

Apply equivalent error-envelope mapping to other transcode clients during integration. Add per-user admission quotas once ownership is tracked (F34); a global cap by itself does not provide fairness against a single authenticated user occupying all slots.

**Regression checks.** Keep eight sessions recently touched and request a ninth. The existing eight must remain alive and the ninth must receive a retryable capacity response. An expired idle session may be reclaimed. Test concurrent admissions to ensure the cap cannot be exceeded.

<a id="f32"></a>

## F32 — A closed transcode manager can create new sessions without a reaper

**Severity: Medium**  
**Evidence classification:** Confirmed lifecycle defect  
**Source at audited revision:** [`internal/transcode/transcode.go`](https://github.com/libteca/libteca/blob/47eb141fdd7565994b1671abd7679bcbdc324439/internal/transcode/transcode.go) · [`internal/server/server.go`](https://github.com/libteca/libteca/blob/47eb141fdd7565994b1671abd7679bcbdc324439/internal/server/server.go)

**Evidence and impact.** `CloseAll` stops the reaper and removes current sessions, but `Get` has no closed-state check. A request racing with or arriving after closure can start a new FFmpeg process in the now-unmanaged manager. The main shutdown race in F11 makes this more consequential.

**Suggested fix and implementation.** Add a closed flag protected by the same mutex as session admission:

```go
// Add to Manager:
// closed bool

var ErrClosed = errors.New("transcode manager closed")
```

At the beginning of the locked portion of `Get`:

```go
if m.closed { return nil, ErrClosed }
```

In `CloseAll`, immediately after acquiring `m.mu`:

```go
m.closed = true
```

Keep shutdown idempotent. Add a process-completion join if `CloseAll` is intended to guarantee child processes have exited, rather than merely received a kill signal: `Session.kill` currently removes the directory immediately after signaling and does not wait for `Session.done`. A bounded wait should happen before reporting cleanup complete; avoid waiting while holding a mutex needed by the process-completion goroutine introduced in F33.

**Regression checks.** `Get` after `CloseAll` must return `ErrClosed`, create no directory, and call no process-spawn hook. Race closure with admissions under `go test -race`. Verify repeated close calls and verify child termination before the enclosing server closes its resources.

<a id="f33"></a>

## F33 — Hardware fallback treats successful short transcodes as failures

**Severity: Medium**  
**Evidence classification:** Confirmed process-result handling defect  
**Source at audited revision:** [`internal/transcode/transcode.go`](https://github.com/libteca/libteca/blob/47eb141fdd7565994b1671abd7679bcbdc324439/internal/transcode/transcode.go)

**Evidence and impact.** The process-wait goroutine discards `p.wait()`'s error. `watchFallback` retries in software whenever the hardware process exits within five seconds, without checking whether it exited successfully. A short clip or quickly completed hardware transcode can be unnecessarily re-encoded into the same session output directory. The exact visible playback effect depends on timing and FFmpeg output; the redundant fallback is established by the control flow.

**Suggested fix and implementation.** Record the exit result for the current process generation and only fall back on an actual failure. Add `exitErr error` to `Session`. In `launch`, publish the process/channel before starting its waiter, and replace the old publication/wait sequence with:

```go
done := make(chan struct{})
s.mu.Lock()
s.proc = p
s.done = done
s.exitErr = nil
killed := s.killed
s.mu.Unlock()
if killed { p.kill() }
go func() {
    err := p.wait()
    s.mu.Lock()
    if s.done == done { s.exitErr = err }
    s.mu.Unlock()
    close(done)
}()
```

This splice goes after a successful `p.start()` and replaces, rather than duplicates, the original waiter and later `s.proc`/`s.done` assignment. After `watchFallback` observes `<-first`, gate fallback on the result:

```go
s.mu.Lock()
killed := s.killed
exitErr := s.exitErr
stillCurrent := s.done == first
s.mu.Unlock()
if killed || !stillCurrent || exitErr == nil { return }
```

Keep the existing one-time fallback and cancellation rules. Consider isolated output generations or an explicit playlist restart for failed partial hardware output; do not assume rewriting already-served segments is seamless. That client-visible behavior needs a media integration test.

**Regression checks.** A fake hardware process that exits successfully inside the fallback window must spawn once. A nonzero exit inside the window must trigger exactly one software retry. Cancellation must not retry. Race very fast process completion against publication to ensure its error cannot be lost.

<a id="f34"></a>

## F34 — Core HLS sessions are not tied to their issuing user or server-issued tickets

**Severity: Medium**  
**Evidence classification:** Confirmed authorization/control-plane gap  
**Source at audited revision:** [`internal/api/core/hls.go`](https://github.com/libteca/libteca/blob/47eb141fdd7565994b1671abd7679bcbdc324439/internal/api/core/hls.go) · [`internal/transcode/transcode.go`](https://github.com/libteca/libteca/blob/47eb141fdd7565994b1671abd7679bcbdc324439/internal/transcode/transcode.go)

**Evidence and impact.** `editionPlayback` generates a session ID but does not register its owner. `hlsFile` accepts a syntactically valid client-constructed `web-<edition>...` ID and starts work from it. `hlsStop` closes any matching session without checking its owner. A user who knows another user's session ID can stop it. IDs have unpredictable suffixes when issued normally, which reduces guessing; that is not a substitute for authorization. The library is intentionally shared, so this is principally session interference/admission control, not proof of access to a forbidden library.

**Suggested fix and implementation.** Register bounded, expiring, user-owned playback tickets when issuing IDs; validate tickets before starting or stopping sessions. Add to core (using existing `sync`, `time`, `fmt`, and `auth` imports):

```go
type webTicket struct {
    userID int64
    editionID int64
    expires time.Time
}
// Add to API:
// ticketMu sync.Mutex
// tickets map[string]webTicket

func (a *API) issueWebTicket(userID, editionID int64) (string, error) {
    sid, err := webSessionID(editionID)
    if err != nil { return "", err }
    a.ticketMu.Lock()
    defer a.ticketMu.Unlock()
    if a.tickets == nil { a.tickets = make(map[string]webTicket) }
    now := time.Now()
    for key, ticket := range a.tickets {
        if !now.Before(ticket.expires) { delete(a.tickets, key) }
    }
    if len(a.tickets) >= 256 { return "", fmt.Errorf("playback ticket capacity exhausted") }
    a.tickets[sid] = webTicket{userID, editionID, now.Add(10*time.Minute)}
    return sid, nil
}

func (a *API) checkWebTicket(sid string, userID int64, remove bool) (int64, bool) {
    a.ticketMu.Lock()
    defer a.ticketMu.Unlock()
    ticket, ok := a.tickets[sid]
    if !ok || ticket.userID != userID { return 0, false }
    if !time.Now().Before(ticket.expires) {
        delete(a.tickets, sid)
        return 0, false
    }
    if remove {
        delete(a.tickets, sid)
    } else {
        ticket.expires = time.Now().Add(10*time.Minute)
        a.tickets[sid] = ticket
    }
    return ticket.editionID, true
}
```

In `editionPlayback`, replace `webSessionID(ed.ID)` with `a.issueWebTicket(auth.UserID(r), ed.ID)`. In `hlsFile`, obtain the edition ID from `checkWebTicket(sid, auth.UserID(r), false)`, not from parsing an arbitrary supplied ID. In `hlsStop`, require `checkWebTicket(sid, auth.UserID(r), true)` before closing; otherwise return 404. Apply F31 for active-process admission and map ticket-capacity errors to a retryable response. Rejecting legacy bare IDs is an intentional core-client contract change; migrate compatible clients rather than silently assuming all clients already use tickets.

An already admitted request may still be in flight when stop occurs. If stop must synchronously prevent even those requests from creating a process, serialize ticket consumption/start/stop or give each ticket a cancellation generation recognized by the manager. Do not claim the lookup helper alone provides that stronger lifecycle guarantee.

**Regression checks.** User B must not read/control user A's ticket; fabricated, expired, and stopped IDs must not spawn processes. User A must continue to retrieve the manifest/segments. Test registry limits, expiry, and stop/start races.

<a id="f35"></a>

## F35 — HLS start positions and non-media editions are not validated strictly

**Severity: Medium**  
**Evidence classification:** Confirmed input/type validation gap  
**Source at audited revision:** [`internal/api/core/hls.go`](https://github.com/libteca/libteca/blob/47eb141fdd7565994b1671abd7679bcbdc324439/internal/api/core/hls.go) · [`README.md`](https://github.com/libteca/libteca/blob/47eb141fdd7565994b1671abd7679bcbdc324439/README.md)

**Evidence and impact.** The `start` query accepts any parsed value greater than zero, which includes positive infinity. Invalid and negative values silently become zero. Playback fallback treats a non-browser-playable edition as transcodable without first establishing it contains supported media; a game/book with no audio/video codec can be routed toward FFmpeg despite games being documented as download-then-play. These are resource/error-handling defects, not command injection: process arguments are passed as an argument slice.

**Suggested fix and implementation.** Reject malformed/non-finite/out-of-range starts, and reject editions with no supported audio/video source before allocating a ticket or process. Add `math` to imports:

```go
start := 0.0
if raw := r.URL.Query().Get("start"); raw != "" {
    value, err := strconv.ParseFloat(raw, 64)
    if err != nil || math.IsNaN(value) || math.IsInf(value, 0) || value < 0 {
        writeJSON(w, 400, map[string]string{"error": "invalid start position"})
        return
    }
    if duration := ed.TotalDuration(); duration > 0 && value > duration {
        writeJSON(w, 400, map[string]string{"error": "start exceeds duration"})
        return
    }
    start = value
}
```

After loading the first file in both playback-decision and HLS-start handlers:

```go
f := ed.Files[0]
hasVideo := f.VideoCodec != nil && *f.VideoCodec != ""
hasAudio := f.Codec != nil && *f.Codec != ""
if strings.HasPrefix(ed.Format, "game-") || (!hasVideo && !hasAudio) {
    writeJSON(w, 415, map[string]string{"error": "edition is not streamable audio/video"})
    return
}
```

Codec/container heuristics still cannot prove actual browser capability. Preserve a client-detected direct-play failure path to HLS and test supported browser/container combinations; this review did not establish a complete device-profile compatibility matrix.

**Regression checks.** Exercise `NaN`, `Inf`, negative, malformed, zero, and excessive start values. Games/EPUB/CBZ data must not start FFmpeg. Confirm an ordinary supported video still starts at the intended position and HLS segment requests reuse the intended session.

<a id="f36"></a>

## F36 — Service worker caches authenticated protocol endpoints outside its denylist

**Severity: High**  
**Evidence classification:** Confirmed cache policy defect; isolated policy reproduction executed  
**Source at audited revision:** [`web/public/sw.js`](https://github.com/libteca/libteca/blob/47eb141fdd7565994b1671abd7679bcbdc324439/web/public/sw.js) · [`internal/api/opds/opds.go`](https://github.com/libteca/libteca/blob/47eb141fdd7565994b1671abd7679bcbdc324439/internal/api/opds/opds.go) · [`internal/api/subsonic/subsonic.go`](https://github.com/libteca/libteca/blob/47eb141fdd7565994b1671abd7679bcbdc324439/internal/api/subsonic/subsonic.go)

**Evidence and impact.** The worker caches every same-origin GET not matched by `NEVER`, not just shell assets. `/rest/*` and `/opds/*` are absent from that list. Controlled browser requests to those routes can be cached and returned without contacting the server, including after logout/revocation. Basic-auth requests to the same URL are especially problematic if the response does not vary by authorization. This affects service-worker-controlled browser traffic, not native clients that never use the worker. An isolated Node check confirmed those route prefixes pass the cache policy.

The same worker also caches any successful navigation under the key `/`, so a protocol/navigation response can contaminate the offline shell. Cache-storage failures are mixed into the network try/catch, which can turn a successful network response into stale fallback or a rejected request. These are related consequences of treating arbitrary responses as static shell assets.

**Suggested fix and implementation.** Replace the denylist with a positive static-asset allowlist, cache only the actual shell route, bypass authorization/query/range requests, and fail open to the network if caching fails. A replacement worker is:

```javascript
const PREFIX = "libteca-static-";
const CACHE = PREFIX + "v5";
const STATIC = new Set([
  "/icon.svg", "/icon-192.png", "/icon-512.png",
  "/icon-maskable-512.png", "/apple-touch-icon.png", "/manifest.webmanifest",
]);

self.addEventListener("install", (event) => {
  event.waitUntil(caches.open(CACHE).then((cache) => cache.add("/")));
  // Do not force an in-use old application to adopt a new worker mid-session.
});

self.addEventListener("activate", (event) => {
  event.waitUntil((async () => {
    const keys = await caches.keys();
    await Promise.all(keys
      .filter((key) => key.startsWith(PREFIX) && key !== CACHE)
      .map((key) => caches.delete(key)));
    // Delete the known old libteca cache names during this one-time migration.
    for (const old of ["libteca-libteca-v1", "libteca-libteca-v2",
                       "libteca-libteca-v3", "libteca-libteca-v4"]) {
      await caches.delete(old);
    }
  })());
});

async function cachedStatic(request, shell) {
  let cache;
  try { cache = await caches.open(CACHE); } catch { /* network still works */ }
  if (!shell && cache) {
    try {
      const hit = await cache.match(request);
      if (hit) return hit;
    } catch { /* network still works */ }
  }
  let response;
  try {
    response = await fetch(request);
  } catch (networkError) {
    if (shell && cache) {
      try {
        const hit = await cache.match("/");
        if (hit) return hit;
      } catch { /* preserve the actual network failure */ }
    }
    throw networkError;
  }
  const noStore = /\bno-store\b/i.test(response.headers.get("Cache-Control") || "");
  const isHTML = /text\/html/i.test(response.headers.get("Content-Type") || "");
  if (cache && response.status === 200 && !noStore && (!shell || isHTML)) {
    try { await cache.put(shell ? "/" : request, response.clone()); }
    catch { /* quota/storage failures must not discard a good network response */ }
  }
  return response;
}

self.addEventListener("fetch", (event) => {
  const request = event.request;
  if (request.method !== "GET") return;
  const url = new URL(request.url);
  if (url.origin !== self.location.origin || url.search) return;
  if (request.headers.has("Authorization") || request.headers.has("Range")) return;
  const shell = url.pathname === "/";
  const asset = url.pathname.startsWith("/assets/") || STATIC.has(url.pathname);
  if (!shell && !asset) return;
  if (request.mode === "navigate" && !shell) return;
  event.respondWith(cachedStatic(request, shell));
});
```

Use a build-derived cache version in production and validate the actual asset output prefix. Keep old deploy assets available while old tabs are open or use an explicit update/reload flow. The exact known v4 cache name is verified by the source; earlier v1–v3 names in this migration are harmless explicit cleanup candidates, not a claim their deployment history was inspected. F37 explains why cleanup must not delete unrelated applications' caches.

**Regression checks.** In a real controlled browser, fetch OPDS under two different users, log out/revoke, and verify protocol requests always reach the network. Navigate to a protocol URL, go offline, and verify it did not replace `/`. Simulate CacheStorage/quota failures: online static responses must still succeed. Test range requests and worker upgrades with multiple open tabs.

<a id="f37"></a>

## F37 — Service-worker activation deletes unrelated origin caches

**Severity: Low**  
**Evidence classification:** Confirmed cache-ownership defect  
**Source at audited revision:** [`web/public/sw.js`](https://github.com/libteca/libteca/blob/47eb141fdd7565994b1671abd7679bcbdc324439/web/public/sw.js)

**Evidence and impact.** Activation deletes every CacheStorage entry whose name is not the current cache. CacheStorage is origin-wide, not scoped to a particular application path. Another application or worker hosted on the same origin can lose its offline data during libteca activation.

**Suggested fix and implementation.** Delete only cache names that libteca owns, and use the current named cache for lookups instead of unrestricted `caches.match`. F36 includes the full replacement. The essential cleanup filter is:

```javascript
const names = await caches.keys();
await Promise.all(names
  .filter((name) => name.startsWith("libteca-static-") && name !== CACHE)
  .map((name) => caches.delete(name)));
```

Use explicit names for legacy migration, not an origin-wide wildcard. Avoid `skipWaiting`/`clients.claim` as an automatic update strategy unless the application can safely handle old tabs loading new chunks.

**Regression checks.** Seed a cache named `another-app-offline`, activate libteca's worker, and verify it survives. Old libteca-owned caches should be removed according to the migration policy, while the active cache remains usable.

<a id="f38"></a>

## F38 — Browser API helper treats failed HTTP responses as successful data

**Severity: Medium**  
**Evidence classification:** Confirmed HTTP-contract defect  
**Source at audited revision:** [`web/src/api.ts`](https://github.com/libteca/libteca/blob/47eb141fdd7565994b1671abd7679bcbdc324439/web/src/api.ts) · [`internal/api/core/core.go`](https://github.com/libteca/libteca/blob/47eb141fdd7565994b1671abd7679bcbdc324439/internal/api/core/core.go)

**Evidence and impact.** `api()` parses and returns JSON without checking `res.ok`, except for its special 401 branch. Core 404 responses deliberately use `application/problem+json` with `detail` and `status`, not the `error` field used by other responses. Consequently, a failed HTTP response can resolve as ordinary data with no normalized error. Whether a particular view displays success depends on that caller; the shared helper itself does not provide a consistent failure contract.

**Suggested fix and implementation.** Preserve the current return-object convention while normalizing every unsuccessful HTTP response. Switching to thrown exceptions is also viable, but requires migrating and testing every caller rather than changing the helper alone. Replace the response-reading block after the existing 401 handling with:

```typescript
const text = await res.text();
let payload: unknown = {};
if (text) {
  try {
    payload = JSON.parse(text);
  } catch {
    return {
      error: res.ok ? "Invalid JSON response from server" : `Server error (${res.status})`,
      status: res.status,
    };
  }
}
if (!res.ok) {
  const problem = payload !== null && typeof payload === "object" && !Array.isArray(payload)
    ? payload as Record<string, unknown>
    : {};
  const message = [problem.error, problem.detail, problem.title]
    .find((value): value is string => typeof value === "string" && value.length > 0);
  return { ...problem, error: message || `Server error (${res.status})`, status: res.status };
}
return payload;
```

A successful empty response remains valid for 204. A stronger follow-on is a typed `ApiResult<T>` discriminated union, with callers required to check success before accessing data. Do not silently make that API-breaking change without updating the views. Independently, handle storage exceptions in the 401 branch so inaccessible local storage does not prevent clearing in-memory credentials.

**Regression checks.** Test a JSON 400, RFC 7807 404, JSON 500, HTML 502, malformed JSON 200, empty 204, and array-valued 200. Every failed response should expose an `error` string and the HTTP status; successful arrays should not be converted into objects. Verify affected views do not display a mutation-success message after an error result.

<a id="f39"></a>

## F39 — Browser request-header merging and response types do not match the API

**Severity: Low**  
**Evidence classification:** Confirmed interface defects  
**Source at audited revision:** [`web/src/api.ts`](https://github.com/libteca/libteca/blob/47eb141fdd7565994b1671abd7679bcbdc324439/web/src/api.ts) · [`internal/api/core/core.go`](https://github.com/libteca/libteca/blob/47eb141fdd7565994b1671abd7679bcbdc324439/internal/api/core/core.go) · [`README.md`](https://github.com/libteca/libteca/blob/47eb141fdd7565994b1671abd7679bcbdc324439/README.md)

**Evidence and impact.** `RequestInit.headers` supports `Headers` objects, tuple arrays, and object records, but the helper spreads `opts.headers` into an object. A `Headers` instance contributes no enumerable header entries; a tuple array contributes numeric object keys rather than the intended header names. An isolated Node check reproduced the `Headers` case. Separately, `Library.path` is mandatory in TypeScript although the server intentionally omits it for non-admins, and `LibraryType` excludes the supported podcast and game library types. These types misdescribe the contract even when a current view happens not to exercise the mismatch.

**Suggested fix and implementation.** Normalize with the platform `Headers` constructor, preserving caller overrides. Replace header construction in `api()`:

```typescript
const headers = new Headers(opts.headers);
if (!headers.has("Content-Type")) headers.set("Content-Type", "application/json");
if (token && !headers.has("Authorization")) {
  headers.set("Authorization", `Bearer ${token}`);
}
const res = await fetch(`/api/core${path}`, { ...opts, headers });
```

Correct the exported contract types, and let TypeScript identify the views that must handle the optional path or additional library kinds:

```typescript
export type LibraryType =
  | "movies" | "tv" | "music" | "audiobooks"
  | "books" | "comics" | "podcasts" | "games";

export type Library = {
  id: number;
  name: string;
  type: LibraryType;
  path?: string;
};
```

Do not expose server paths to ordinary users just to satisfy the old type. Where library kinds are intentionally restricted in a UI, define a narrower input type for that UI rather than misrepresenting the server response.

**Regression checks.** Exercise all three `HeadersInit` forms, including custom authorization. Fetch the library list as admin and ordinary user. Compile exhaustive library-kind handling after adding podcasts and games; verify unsupported player choices are not inferred from the broader type alone.

<a id="f40"></a>

## F40 — EPUB percentage-only resume fails when location indexes are already cached

**Severity: Medium**  
**Evidence classification:** Confirmed reader logic defect  
**Source at audited revision:** [`web/src/reader/epub.tsx`](https://github.com/libteca/libteca/blob/47eb141fdd7565994b1671abd7679bcbdc324439/web/src/reader/epub.tsx)

**Evidence and impact.** The percentage-resume branch requires `!haveLocations`. When a valid location index is loaded from local storage and server progress contains a percentage but no valid CFI, that branch is skipped. `target` remains undefined and `rendition.display(target)` opens the beginning. A subsequent relocation save can replace the previously useful resume state. This is a branch-condition defect, not a speculative EPUB-library incompatibility; an isolated Node check reproduced the missing-target condition.

**Suggested fix and implementation.** Only location *generation* should depend on the cache being absent. Mapping the saved percentage to a CFI must occur with either a cached or newly generated index. Replace the target-selection block with:

```typescript
let target: string | undefined;
const p = props.progress;
if (p && !p.isFinished && p.locator?.startsWith("epubcfi(")) {
  target = p.locator;
} else if (p && !p.isFinished && typeof p.percent === "number" &&
           Number.isFinite(p.percent) && p.percent > 0 && p.percent < 1) {
  if (!haveLocations) {
    setPhase("indexing");
    await book.locations.generate(LOC_CHUNK);
    if (destroyed) {
      try { book.destroy(); } catch { /* already destroyed */ }
      return;
    }
    haveLocations = true;
    locsReadyRef.current = true;
    persistLocations(book);
  }
  target = book.locations.cfiFromPercentage(p.percent);
}
```

Keep the existing display-error fallback, but surface a non-disruptive warning when a saved locator cannot be restored rather than silently claiming an exact resume. In-place book replacement also calls for a content-versioned location-cache key; that requires a server revision field and is an additional design improvement, not part of the demonstrated branch fix.

**Regression checks.** Cover percentage-only progress with and without cached locations; both must select an equivalent target. Test valid CFI precedence, missing progress, finished books, corrupt cached indexes, and a failed saved-CFI display. Assert that initial relocation does not overwrite progress before restoration has been attempted.

<a id="f41"></a>

## F41 — EPUB loading continues after unmount and carries stale state across editions

**Severity: Low**  
**Evidence classification:** Confirmed cancellation and UI-state gaps  
**Source at audited revision:** [`web/src/reader/epub.tsx`](https://github.com/libteca/libteca/blob/47eb141fdd7565994b1671abd7679bcbdc324439/web/src/reader/epub.tsx)

**Evidence and impact.** The effect fetches the complete EPUB and consumes an `ArrayBuffer` without an abort signal. Cleanup sets `destroyed`, but only checks that flag after the network/body work, so leaving the reader does not cancel a large transfer. The effect reruns for a different `editionId` without first clearing phase, errors, table of contents, percentage, or section state. Reusing the component for another edition can therefore display old loading/reader information while the next book opens.

**Suggested fix and implementation.** Add cancellation and explicit reset at the start of the existing edition-scoped effect; keep the existing book/rendition destruction and post-await guards. The relevant splices are:

```typescript
// At the start of the effect, before launching the async load:
const controller = new AbortController();
setPhase("loading");
setError("");
setToc([]);
setTocOpen(false);
setPercent(null);
setSectionHref("");
locsReadyRef.current = false;

// Replace the existing fetch call:
const res = await fetch(media(`/editions/${props.editionId}/download`), {
  signal: controller.signal,
});

// In the catch block, do not display an error for intentional cancellation:
if (!destroyed && !controller.signal.aborted) {
  setError(String((err as Error)?.message || err));
  setPhase("error");
}

// In the existing effect cleanup, before destroying book/rendition:
destroyed = true;
controller.abort();
```

These are insertion points within one effect, not a standalone block to paste at module scope. Do not remove cleanup of keyboard listeners or existing `destroy()` calls. Apply the same cancellation pattern to other readers only after reviewing their own lifecycles; those implementations were not established by this finding.

**Regression checks.** Unmount while the HTTP body is still transferring and assert it is aborted. Switch editions without remounting the parent; the new book must start in loading state with no old contents/percentage. An intentional abort must not show a reader-error overlay.

<a id="f42"></a>

## F42 — Subsonic queue and metadata endpoints return success without implementing the operation

**Severity: Medium**  
**Evidence classification:** Confirmed false-success behavior  
**Source at audited revision:** [`internal/api/subsonic/subsonic.go`](https://github.com/libteca/libteca/blob/47eb141fdd7565994b1671abd7679bcbdc324439/internal/api/subsonic/subsonic.go)

**Evidence and impact.** `savePlayQueue`, `getPlayQueue`, `getStarred2`, and `getAlbumInfo2` simply return `ok()`. In particular, `savePlayQueue` accepts a mutation and reports success without persisting anything. The read endpoints omit the requested result payload instead of clearly distinguishing unsupported functionality. This can cause clients to trust a save that never occurred. Exact behavior of each native client remains unverified; the server's no-op mutation is directly visible.

**Suggested fix and implementation.** Until persistence and the required response structures exist, explicitly return the protocol's unsupported-operation error. Keeping HTTP 200 for a Subsonic error envelope is intentional and is not itself a defect. Replace the four stubs with:

```go
func (a *API) savePlayQueue(w http.ResponseWriter, r *http.Request, _ int64) {
    a.respond(w, r, errResponse(errNotImplemented, "Play queue persistence is not implemented"))
}
func (a *API) getPlayQueue(w http.ResponseWriter, r *http.Request, _ int64) {
    a.respond(w, r, errResponse(errNotImplemented, "Play queue retrieval is not implemented"))
}
func (a *API) getStarred2(w http.ResponseWriter, r *http.Request, _ int64) {
    a.respond(w, r, errResponse(errNotImplemented, "Starred-item retrieval is not implemented"))
}
func (a *API) getAlbumInfo2(w http.ResponseWriter, r *http.Request, _ int64) {
    a.respond(w, r, errResponse(errNotImplemented, "Album information is not implemented"))
}
```

If empty starred items or absent external album information are valid product states, implement their valid empty payloads instead of using generic `ok()`. Queue support requires user-scoped ordered item persistence and current-item/position state in a transaction; unsupported is safer than a fictitious successful save while that work is incomplete.

**Regression checks.** A save must either survive a server restart and round-trip through retrieval, or return an explicit unsupported error. Test both XML and JSON envelopes, and record actual client behavior before advertising compatibility.

<a id="f43"></a>

## F43 — Checked-in CI does not enforce race testing or locked frontend installation

**Severity: Low**  
**Evidence classification:** Verified CI assurance and reproducibility gaps  
**Source at audited revision:** [`.github/workflows/ci.yml`](https://github.com/libteca/libteca/blob/47eb141fdd7565994b1671abd7679bcbdc324439/.github/workflows/ci.yml) · [`go.mod`](https://github.com/libteca/libteca/blob/47eb141fdd7565994b1671abd7679bcbdc324439/go.mod)

**Evidence and impact.** The inspected workflow runs `go vet` and ordinary `go test`, but not `-race`; the web job uses `npm install` rather than `npm ci`. Thus this workflow does not enforce race detection or a frozen lockfile install despite the concurrency-sensitive server code. It also does not explicitly provision FFmpeg for integration tests or run browser behavioral regressions. This is a statement about the checked-in workflow, not a claim that maintainers never run additional tests elsewhere or that every test currently fails.

**Suggested fix and implementation.** Use the module's declared Go version, reproducible npm installation, explicit dependencies, finite job timeouts, and least-privilege workflow permissions. A replacement baseline workflow is:

```yaml
name: CI
on:
  push:
    branches: [main]
  pull_request:
permissions:
  contents: read

jobs:
  go:
    runs-on: ubuntu-latest
    timeout-minutes: 20
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version-file: go.mod
          cache: true
      - run: sudo apt-get update && sudo apt-get install -y ffmpeg
      - run: go vet ./...
      - run: go test -race ./... -timeout 600s

  web:
    runs-on: ubuntu-latest
    timeout-minutes: 15
    defaults:
      run:
        working-directory: web
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-node@v4
        with:
          node-version: "22"
          cache: npm
          cache-dependency-path: web/package-lock.json
      - run: npm ci
      - run: npx tsc --noEmit
      - run: npm run build
```

The existing action major versions are retained here, not certified as the newest releases. Pin them to independently verified immutable commit SHAs under the project's update policy; do not invent action hashes. Add deterministic concurrency tests from F01/F03/F11/F32 and a browser harness for F36–F41. Keep parser fuzzing, dependency vulnerability scanning, and native-client corpus replay as explicit additional CI work rather than claiming this YAML performs them. Dependency CVEs and branch-protection settings were not assessed.

**Regression checks.** A lockfile/package manifest mismatch must fail the frontend install. A deliberately introduced race in a test fixture should fail the Go job. Ensure media tests actually execute when FFmpeg is available rather than silently skipping. Verify job permissions and runtime on the supported release platforms separately.

<a id="f44"></a>

## F44 — Repeated handler construction recreates destructive state and leaks backend ownership

**Severity: Low**  
**Evidence classification:** Confirmed lifecycle defect under repeated construction  
**Source at audited revision:** [`internal/server/server.go`](https://github.com/libteca/libteca/blob/47eb141fdd7565994b1671abd7679bcbdc324439/internal/server/server.go) · [`internal/transcode/transcode.go`](https://github.com/libteca/libteca/blob/47eb141fdd7565994b1671abd7679bcbdc324439/internal/transcode/transcode.go) · [`internal/api/core/hls.go`](https://github.com/libteca/libteca/blob/47eb141fdd7565994b1671abd7679bcbdc324439/internal/api/core/hls.go)

**Evidence and impact.** Every `Server.Handler()` call creates another transcode manager; `transcode.New` removes the shared transcode directory and starts a reaper. The server then overwrites its stored manager/Jellyfin pointers. Repeated construction can disrupt the previous handler's streams and leave its manager outside the later `Server.Close()` ownership path. Normal `main` calls the method once, so this is an embedding/test/reconfiguration hazard, not a claim that every startup creates multiple managers. Separately, the package-global trickplay map retains `*API` keys indefinitely.

**Suggested fix and implementation.** Make construction idempotent for a given server. Add `sync` to the server imports and these fields/methods; rename the existing handler-building implementation to `buildHandler`:

```go
// Fields added to Server:
// handlerOnce sync.Once
// handler http.Handler

func (s *Server) Handler() http.Handler {
    s.handlerOnce.Do(func() {
        s.handler = s.buildHandler()
    })
    return s.handler
}
```

Finalize configuration such as `HWAccel` before first construction and do not mutate it concurrently afterward. To eliminate the trickplay map's global retention, add the following fields to `core.API` and replace its global `tpMu`/`tpGen` machinery:

```go
// Fields added to core.API:
// tpMu sync.Mutex
// tp *trickplay.Generator

func (a *API) trickplayer() *trickplay.Generator {
    a.tpMu.Lock()
    defer a.tpMu.Unlock()
    if a.tp == nil {
        a.tp = trickplay.New(a.DataDir)
    }
    return a.tp
}
```

Add the `trickplay` import to the file declaring `API`, and remove now-unused imports from `hls.go`. This fixes ownership/retention, not an unverified generator goroutine leak. Use a fresh server instance for intentional reconfiguration, with an explicit lifecycle rather than rerunning `Handler()`.

**Regression checks.** Call `Handler()` twice while a session is active; both calls must return the same handler and must not delete segments or start another reaper. Close the server once and verify every owned manager is closed. Create/drop API instances and ensure no package-global map holds them alive.

<a id="f45"></a>

## F45 — Unchanged podcast feeds prevent retrying failed episode downloads

**Severity: Medium**  
**Evidence classification:** Confirmed retry-state defect  
**Source at audited revision:** [`internal/podcast/podcast.go`](https://github.com/libteca/libteca/blob/47eb141fdd7565994b1671abd7679bcbdc324439/internal/podcast/podcast.go) · [`internal/podcast/download.go`](https://github.com/libteca/libteca/blob/47eb141fdd7565994b1671abd7679bcbdc324439/internal/podcast/download.go)

**Evidence and impact.** `downloadPending` only logs download failures and returns no error. Failed episodes remain pending, but `refresh` returns immediately on HTTP 304 before running pending-download processing. A temporary enclosure failure can therefore remain unretried until the feed representation changes, even though it is still a valid pending task. This is separate from F26: fixing validator commit order does not make the existing 304 early return process a download queue.

**Suggested fix and implementation.** Treat episode delivery as work independent of whether feed metadata changed. Replace `downloadPending` with a checked version (add the `errors` import):

```go
func (s *Service) downloadPending(ctx context.Context, p *store.Podcast) error {
    pending, err := s.DB.PendingEpisodes(p.ID)
    if err != nil { return err }
    var failures []error
    for i := range pending {
        if err := ctx.Err(); err != nil {
            failures = append(failures, err)
            break
        }
        if i < p.MaxEpisodes {
            if err := s.downloadEpisode(ctx, p, &pending[i]); err != nil {
                failures = append(failures,
                    fmt.Errorf("episode %d download: %w", pending[i].ID, err))
            }
        } else if err := s.DB.MarkEpisodeSeen(pending[i].ID); err != nil {
            failures = append(failures,
                fmt.Errorf("episode %d mark seen: %w", pending[i].ID, err))
        }
    }
    return errors.Join(failures...)
}
```

Update its callers to inspect the returned error. In `refresh`'s unchanged-feed branch, run pending work even when no metadata needs applying:

```go
if !changed {
    if p.AutoDownload {
        if err := s.downloadPending(ctx, p); err != nil {
            return p, false, err
        }
    }
    if err := s.enforceRetention(p); err != nil {
        return p, false, err
    }
    if err := s.DB.UpdatePodcastFetch(
        p.ID, nilOrEmpty(newETag), nilOrEmpty(newMod), nowMs(),
    ); err != nil {
        return p, false, err
    }
    return p, false, nil
}
```

Combine this with F26 rather than adding a second unchecked validator update. Check the store method signatures during integration; the shown calls use the methods already called by the inspected service. Keep metadata upserts idempotent. The cleaner long-term design commits feed metadata and validators together, and maintains download retries/backoff as a separate persistent queue. The provided minimal patch retries pending work on 304 and surfaces failures; it does not implement a complete scheduled retry/backoff queue.

**Regression checks.** Start with a feed whose first episode download fails, then make the enclosure succeed while the feed returns 304. The next refresh must download it without needing a new feed ETag. Verify auto-download-off does not trigger a fetch, cancellation stops work, and database/download errors are returned rather than only logged. Exercise retention after a recovered download.

---

# Remediation sequencing and integration notes

## First: credential and lifecycle boundaries

Implement F01–F03 as a coordinated authentication change: eliminate cached authorization, retain only current-hash-scoped OPDS verification proofs, make password rotation transactional, and condition token issuance on the credential that actually authorized it. Then migrate bearer-token storage in F09, updating every issuer and lookup consistently. A hash migration with only half the readers changed would lock out valid sessions. F05–F07 should share one request-time password-verification path and one process-wide resource budget. F04 intentionally changes legacy Subsonic authentication unless a separate scoped-app-secret design is implemented; communicate that compatibility change rather than silently retaining primary passwords.

Replace the service worker with F36/F37's allowlist-based version and explicitly retire its known old cache. Test an upgrade with already-open tabs and an already-controlling worker; changing server headers alone does not delete previously cached responses.

Fix main's exit ordering in F11 together with the transcode closed-state guard in F32. Register and cancel every server-owned worker, including work launched outside request handlers, before claiming graceful shutdown is complete. The helper in F11 does not automatically locate or wrap all goroutine launches.

## Second: data and storage integrity

Apply the download cap in F27 and the legacy-safe ID directory namespace in F28 before additional untrusted feeds are ingested. Treat aggregate disk quota/reservations as a separate required deployment policy. Coordinate F26 and F45 so metadata validators are not advanced ahead of failed ingestion and pending downloads can retry without a feed change.

For backups, choose either F14's minimally disruptive no-overwrite database publication or F15's preferable complete-generation layout. Do not mix a generation-directory writer with the old flat-file-only pruning/restore assumptions. F15 deliberately does not claim an atomic database-plus-files snapshot while cover writers remain uncoordinated; content-addressed immutable covers or a shared mutation/snapshot lock are needed for that stronger guarantee. Verify a restore, not just backup file creation. Apply F16 to the chosen layout.

F29 is a latent defect in the general store deletion method, plus a nil-service HTTP false-success case. The normal dedicated podcast service already captures file IDs correctly. Preserve that existing good path rather than replacing it with the defective general helper.

## Third: reliable contracts and user-visible correctness

Improve read/error and serialization handling together (F19/F20/F38), so backend failures do not become apparently valid empty data in the browser. Scope work-detail queries (F21), validate progress and media input (F22/F35), and fix EPUB restoration/cancellation (F40/F41). Introduce HLS ownership/ticket checks in F34 alongside explicit session capacity rejection in F31, avoiding accidental termination of another viewer's active session.

F43 supplies a stronger CI baseline, not a complete verification program. The decisive acceptance tests are the regression cases attached to each finding: revocation/rotation interleavings, interrupted shutdown, backup collision and restore, podcast failure recovery, browser credential-cache isolation, and actual playback behavior. Maintain an explicit compatibility matrix with recorded traffic and live tests before asserting support for inherited clients.

## Applying the code safely

Snippets labeled “add fields,” “replace block,” or “splice” are not standalone source files. Add the named imports, remove imports made unused by replacement, and update all call sites when a function signature changes. Preserve existing protocol-specific response envelopes and route mounting. Values such as two KDF slots, a 2-GiB episode ceiling, token lifetime, and playback-ticket expiry are example policies to tune and test, not universal requirements.

Do not directly apply destructive SQL or backup-layout migrations to the only copy of a production library. Use a tested restore copy, run schema/foreign-key checks, and preserve rollback-compatible backups. The proposed fixes were not committed to the GitHub repository.

# Verification record

## Executed checks

| Check | Observed result | What it does not prove |
|---|---|---|
| Python standard-library SQLite fixture for general podcast-library deletion | Original delete ordering left one orphan file row; captured-ID ordering left zero. | It does not exercise the full migrations, normal dedicated podcast service, or production HTTP route. |
| Python model of the episode filename fallback | Episode 3 selected `A-3.mp3`, already owned by episode 2. | It does not run an actual HTTP download or filesystem overwrite. |
| Node model of the service-worker URL predicate | `/rest/*` and `/opds/*` pass the original denylist; the static allowlist excludes them. | It does not run a real browser service worker or demonstrate an end-to-end credential leak. |
| Node model of EPUB target selection | Cached locations plus percentage-only progress left the target undefined. | It does not instantiate EPUB.js or measure browser relocation behavior. |
| Node `Headers` spread check | Spreading a `Headers` instance yielded zero header entries. | It does not exercise every existing API caller. |

The small checks and exact outputs are included below so this report remains self-contained. They validate narrow failure mechanisms, not all proposed remediation.

### Python / SQLite reproduction

```python
import sqlite3,json

def fixture():
    d=sqlite3.connect(':memory:')
    d.execute('PRAGMA foreign_keys=ON')
    d.executescript('''CREATE TABLE libraries(id INTEGER PRIMARY KEY);
CREATE TABLE podcasts(id INTEGER PRIMARY KEY, library_id INTEGER REFERENCES libraries(id));
CREATE TABLE files(id INTEGER PRIMARY KEY, edition_id INTEGER);
CREATE TABLE podcast_episodes(id INTEGER PRIMARY KEY, podcast_id INTEGER REFERENCES podcasts(id), file_id INTEGER REFERENCES files(id));
INSERT INTO libraries VALUES(1); INSERT INTO podcasts VALUES(10,1); INSERT INTO files VALUES(100,NULL); INSERT INTO podcast_episodes VALUES(20,10,100);''')
    return d
x=fixture()
x.execute('DELETE FROM podcast_episodes WHERE podcast_id IN (SELECT id FROM podcasts WHERE library_id=?)',(1,))
x.execute('DELETE FROM files WHERE id IN (SELECT file_id FROM podcast_episodes WHERE podcast_id IN (SELECT id FROM podcasts WHERE library_id=?) AND file_id IS NOT NULL)',(1,))
y=fixture()
ids=[r[0] for r in y.execute('SELECT file_id FROM podcast_episodes WHERE podcast_id IN (SELECT id FROM podcasts WHERE library_id=?) AND file_id IS NOT NULL',(1,))]
y.execute('DELETE FROM podcast_episodes WHERE podcast_id IN (SELECT id FROM podcasts WHERE library_id=?)',(1,))
for i in ids:y.execute('DELETE FROM files WHERE id=?',(i,))
# Filename algorithm, with rows returned by EpisodeUsingFilePath.
paths={'A.mp3':1,'A-3.mp3':2}
name='A'; episode_id=3
path=name+'.mp3'
if path in paths and paths[path]!=episode_id:path=f'{name}-{episode_id}.mp3'
print(json.dumps({'podcast_delete_original_remaining_files':x.execute('SELECT count(*) FROM files').fetchone()[0], 'podcast_delete_snapshot_fix_remaining_files':y.execute('SELECT count(*) FROM files').fetchone()[0], 'episode_3_selected_filename':path, 'selected_filename_already_owned_by_episode':paths.get(path)},indent=2))
```

Observed output:

```json
{
  "podcast_delete_original_remaining_files": 1,
  "podcast_delete_snapshot_fix_remaining_files": 0,
  "episode_3_selected_filename": "A-3.mp3",
  "selected_filename_already_owned_by_episode": 2
}
```

### Node reproduction

```javascript
const NEVER = [/\/api\//, /^\/s\//, /^\/stream\//, /^\/covers\//, /^\/subtitles\//, /^\/Videos\//, /^\/Audio\//];
const paths = ['/api/core/me','/rest/getPlayQueue.view','/opds/all','/Items','/Users/Me'];
const original = Object.fromEntries(paths.map(p=>[p, !NEVER.some(r=>r.test(p))]));
const fix = Object.fromEntries(paths.map(p=>[p, p==='/' || p.startsWith('/assets/')]));
const originalResume = (haveLocations,pct) => pct>0&&pct<1&&!haveLocations ? 'restore percentage' : 'display beginning';
console.log(JSON.stringify({service_worker_intercepts_original:original,service_worker_intercepts_allowlist:fix,epub_percentage_resume_with_cached_locations:originalResume(true,0.45),headers_object_spread_count:Object.keys({...new Headers({Accept:'application/json'})}).length},null,2));
```

Observed output:

```json
{
  "service_worker_intercepts_original": {
    "/api/core/me": false,
    "/rest/getPlayQueue.view": true,
    "/opds/all": true,
    "/Items": true,
    "/Users/Me": true
  },
  "service_worker_intercepts_allowlist": {
    "/api/core/me": false,
    "/rest/getPlayQueue.view": false,
    "/opds/all": false,
    "/Items": false,
    "/Users/Me": false
  },
  "epub_percentage_resume_with_cached_locations": "display beginning",
  "headers_object_spread_count": 0
}
```

## Not executed

No repository-wide compilation, `go test`, `go test -race`, `go vet`, actual npm build/typecheck of the repository, parser fuzz campaign, dependency CVE scan, live server penetration test, database migration/restore drill, hardware-transcode test, native-client corpus replay, or real-browser service-worker/EPUB test was completed in this review. The code examples must not be treated as passing those checks.

Once the changes are integrated in an environment with the required toolchain and dependencies, a baseline verification sequence is:

```sh
# From the repository root, using the Go version required by go.mod.
go version
ffmpeg -version
ffprobe -version
(
  cd web
  npm ci
  npx tsc --noEmit
  npm run build
)
go vet ./...
go test -race ./... -timeout 600s
```

These commands do not replace the targeted tests, browser harness, migration checks, media fixtures, or restore drills described in the findings. Any skipped media or corpus tests must remain visible in release evidence.

# Review coverage and source manifest

The following files or sections were directly read through GitHub at the pinned commit. Listing a file here means its source informed the audit, not that every behavior, dependency, caller, or test around it was exhaustively validated. A tree listing is not counted as a source review. References in each finding point to the affected implementation; function names in the evidence identify the relevant code without relying on moving branch line numbers.

| Source | Reviewed scope |
|---|---|
| [`cmd/libteca/main.go`](https://github.com/libteca/libteca/blob/47eb141fdd7565994b1671abd7679bcbdc324439/cmd/libteca/main.go) | Full source: CLI setup, startup, shutdown, backup command. |
| [`internal/server/server.go`](https://github.com/libteca/libteca/blob/47eb141fdd7565994b1671abd7679bcbdc324439/internal/server/server.go) | Full source: route assembly and owned manager lifecycle. |
| [`internal/auth/auth.go`](https://github.com/libteca/libteca/blob/47eb141fdd7565994b1671abd7679bcbdc324439/internal/auth/auth.go) | Full source: password hashing/verification, token issue/cache, middleware, bootstrap. |
| [`internal/auth/limiter.go`](https://github.com/libteca/libteca/blob/47eb141fdd7565994b1671abd7679bcbdc324439/internal/auth/limiter.go) | Full source: bucket policy and request address handling. |
| [`internal/api/core/core.go`](https://github.com/libteca/libteca/blob/47eb141fdd7565994b1671abd7679bcbdc324439/internal/api/core/core.go) | Retrieved login/library/scan, event, work/progress, and file-serving sections; an initial large response was truncated. Not a claim that every line was reviewed. |
| [`internal/api/core/users.go`](https://github.com/libteca/libteca/blob/47eb141fdd7565994b1671abd7679bcbdc324439/internal/api/core/users.go) | Full source: user and token mutation handlers. |
| [`internal/api/core/libraries.go`](https://github.com/libteca/libteca/blob/47eb141fdd7565994b1671abd7679bcbdc324439/internal/api/core/libraries.go) | Full source: library deletion dispatch and nil-service behavior. |
| [`internal/api/core/hls.go`](https://github.com/libteca/libteca/blob/47eb141fdd7565994b1671abd7679bcbdc324439/internal/api/core/hls.go) | Full source: playback classification, HLS session routing, start/stop, thumbnails. |
| [`internal/api/opds/opds.go`](https://github.com/libteca/libteca/blob/47eb141fdd7565994b1671abd7679bcbdc324439/internal/api/opds/opds.go) | Lines 1–280: Basic authentication, cache, initial feed structures and pagination. |
| [`internal/api/subsonic/subsonic.go`](https://github.com/libteca/libteca/blob/47eb141fdd7565994b1671abd7679bcbdc324439/internal/api/subsonic/subsonic.go) | Lines 1–300: route/authentication wrappers, password persistence, stub endpoints, initial artist handling. |
| [`internal/store/store.go`](https://github.com/libteca/libteca/blob/47eb141fdd7565994b1671abd7679bcbdc324439/internal/store/store.go) | Full source: DSN, pool ownership, migration setup, transaction policy, backfill. |
| [`internal/store/users.go`](https://github.com/libteca/libteca/blob/47eb141fdd7565994b1671abd7679bcbdc324439/internal/store/users.go) | Full source: password, token, and user deletion persistence. |
| [`internal/store/backup.go`](https://github.com/libteca/libteca/blob/47eb141fdd7565994b1671abd7679bcbdc324439/internal/store/backup.go) | Full source: database snapshots, covers copying, pruning. |
| [`internal/store/queries.go`](https://github.com/libteca/libteca/blob/47eb141fdd7565994b1671abd7679bcbdc324439/internal/store/queries.go) | Lines 1–250: library/user/work/edition paths and start of file persistence. |
| [`internal/store/works.go`](https://github.com/libteca/libteca/blob/47eb141fdd7565994b1671abd7679bcbdc324439/internal/store/works.go) | Full source: work/edition/file view loading and error propagation. |
| [`internal/store/reading.go`](https://github.com/libteca/libteca/blob/47eb141fdd7565994b1671abd7679bcbdc324439/internal/store/reading.go) | Full source: reading progress, page counts, edition files, file metadata. |
| [`internal/store/podcasts.go`](https://github.com/libteca/libteca/blob/47eb141fdd7565994b1671abd7679bcbdc324439/internal/store/podcasts.go) | Lines 1–600: podcast/file methods, dedicated deletion paths, pending/seen state, progress. |
| [`internal/store/types.go`](https://github.com/libteca/libteca/blob/47eb141fdd7565994b1671abd7679bcbdc324439/internal/store/types.go) | Full source: pointer/nullability and shared data structures. |
| [`internal/store/meta.go`](https://github.com/libteca/libteca/blob/47eb141fdd7565994b1671abd7679bcbdc324439/internal/store/meta.go) | Selected tail, lines 150 onward: genres and episode metadata helpers; not a full provider audit. |
| [`internal/podcast/fetch.go`](https://github.com/libteca/libteca/blob/47eb141fdd7565994b1671abd7679bcbdc324439/internal/podcast/fetch.go) | Full source: HTTP feed/auxiliary fetch, RSS parsing, duration parsing. |
| [`internal/podcast/podcast.go`](https://github.com/libteca/libteca/blob/47eb141fdd7565994b1671abd7679bcbdc324439/internal/podcast/podcast.go) | Lines 1–250: public HTTP client, service initialization, subscribe, refresh, start of ingestion. |
| [`internal/podcast/download.go`](https://github.com/libteca/libteca/blob/47eb141fdd7565994b1671abd7679bcbdc324439/internal/podcast/download.go) | Full source: download naming/copying/deadlines, retention, sanitization. |
| [`internal/transcode/transcode.go`](https://github.com/libteca/libteca/blob/47eb141fdd7565994b1671abd7679bcbdc324439/internal/transcode/transcode.go) | Lines 1–380: manager/session admission, process lifecycle/fallback, closure/reaper, start of buffering. |
| [`web/src/api.ts`](https://github.com/libteca/libteca/blob/47eb141fdd7565994b1671abd7679bcbdc324439/web/src/api.ts) | Full source: response types, request/response wrapper, query-token media URLs. |
| [`web/public/sw.js`](https://github.com/libteca/libteca/blob/47eb141fdd7565994b1671abd7679bcbdc324439/web/public/sw.js) | Full source: installation, activation, request filtering and caching. |
| [`web/src/reader/epub.tsx`](https://github.com/libteca/libteca/blob/47eb141fdd7565994b1671abd7679bcbdc324439/web/src/reader/epub.tsx) | Lines 1–240: loading/restoration/lifecycle and most reader markup. |
| [`.github/workflows/ci.yml`](https://github.com/libteca/libteca/blob/47eb141fdd7565994b1671abd7679bcbdc324439/.github/workflows/ci.yml) | Full workflow source; no assertion about branch protection or out-of-band jobs. |
| [`go.mod`](https://github.com/libteca/libteca/blob/47eb141fdd7565994b1671abd7679bcbdc324439/go.mod) | Full module declaration/dependency versions; not a vulnerability scan. |
| [`README.md`](https://github.com/libteca/libteca/blob/47eb141fdd7565994b1671abd7679bcbdc324439/README.md) | Product/deployment/compatibility context; not used as proof that implementations work. |

**Important coverage boundaries.** Scanner/parser internals beyond the inspected RSS path, watcher internals, importers, all metadata providers, the full Jellyfin and Audiobookshelf adapters, remaining OPDS/Subsonic routes, all SQL migrations, the remaining players/readers/views, deployment/container/release scripts, and the full test corpus were not comprehensively audited. Dependency internals and live third-party services were not comprehensively audited either. Additional issues may exist in those areas or in interactions not exercised here. The report must not be interpreted as a security certification for unreviewed code.

# Existing safeguards and claims not established

The code already uses parameterized SQL in the inspected query paths, random bearer values, Argon2id password hashing, admin checks on inspected administrative handlers, path-component validation for transcode sessions, and a transcode session-count limit. The podcast HTTP client includes address validation, redirect checks, and disabled proxy inheritance. Those existing protections matter; this report does not claim they are absent. See the linked [authentication](https://github.com/libteca/libteca/blob/47eb141fdd7565994b1671abd7679bcbdc324439/internal/auth/auth.go), [user handlers](https://github.com/libteca/libteca/blob/47eb141fdd7565994b1671abd7679bcbdc324439/internal/api/core/users.go), [transcode manager](https://github.com/libteca/libteca/blob/47eb141fdd7565994b1671abd7679bcbdc324439/internal/transcode/transcode.go), and [podcast client](https://github.com/libteca/libteca/blob/47eb141fdd7565994b1671abd7679bcbdc324439/internal/podcast/podcast.go).

No unauthenticated arbitrary filesystem traversal, SQL injection, generalized server-side request-forgery bypass, token-forging exploit, or confirmed current dependency CVE was established. A stored path plus ordinary `os.Open` is not by itself evidence of remote path traversal. File-system hardening recommendations must state whether an attacker can modify local library/covers directories. JSON request decoding does not accept literal NaN/Infinity; F30 concerns a different parser ingress. `copyTree`'s callback-local deferred closes are not a whole-walk descriptor leak. The normal dedicated podcast deletion path already handles file-ID capture correctly; F29 specifically distinguishes the defective general helper and the nil-service case.

The [README's compatibility matrix](https://github.com/libteca/libteca/blob/47eb141fdd7565994b1671abd7679bcbdc324439/README.md) explicitly leaves inherited-client verification pending. This review neither upgrades those cells to “works” nor declares every native client broken without testing. Likewise, architectural choices such as local-first deployment, unsupported protocol features, and generation-based backups need explicit product decisions; the findings explain where current behavior silently misleads callers or weakens a stated boundary.

---

**End of report.** All 45 findings, proposed implementation examples, source references, isolated reproduction code/results, and verification limits are contained in this file.
