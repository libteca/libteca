# omilator client contract (PLAN-GAMES G3)

The core API is the games face — there is no named protocol because omilator
is the only client. Everything below is served by existing endpoints; the
contract is the enumeration plus the guarantees each endpoint carries.

## Endpoints

| Step | Call | Notes |
|---|---|---|
| Auth | `POST /api/core/login` `{username, password}` → `{token}` | Bearer token for all calls |
| Libraries | `GET /api/core/libraries` | Filter client-side on `type == "games"` |
| Games | `GET /api/core/libraries/{id}/works?sort=title&limit=&offset=` | Works = games; each edition's `format` is `game-<platform>`, `title` is the platform display name, `files` count included |
| Game detail | `GET /api/core/works/{id}` | Editions with `id`, `format`, `title` |
| Cover | `GET /api/core/covers/{cover}` | Present when `hasCover` |
| Download | `GET /api/core/stream/{fileId}` | Range supported (see guarantees); ROM bytes |
| Playtime | `POST /api/core/progress/{editionId}` | Reuse of the reading-progress domain: one position per platform edition |

## Guarantees

- **Range on stream:** verified against a >1 GiB zero-filled file (2026-09-13,
  live binary): `206` with the exact requested bytes on head (`0-1023` →
  1024) and tail (`1073741824-` → the final 8 bytes), and a full download
  returns `200` with the exact total (1,073,741,832 bytes).
- **Auth:** every endpoint above requires the Bearer token; games endpoints
  are on no unauthenticated face.
- **ROM-file identity:** `files.id` is stable across rescans (upserts key on
  path); client-side download caching should key on `(fileId, size)`.
- **Playtime semantics:** `progress.position` is seconds played — treat it
  as a last-played + play-seconds counter; save-state sync is explicitly
  out of scope (PLAN-GAMES §4).

## Out of scope (binding, PLAN-GAMES §2/§4)

Save-state sync, streaming playback, transcode, any face protocol beyond
this core API.
