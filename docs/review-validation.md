# Review validation

Date: 2026-10-03. Baseline: fbf2f20c291533216cb6a21699e40729622ec4f9.

## Executed

- Go 1.26.6 linux/amd64; GCC 14 race support; SQLite via project dependency
- Node 24.19.0 / npm 11.9.0 locally (CI/container recipe selects Node 22)
- ffmpeg/ffprobe 7.1.5, including synthetic-media generation, probing and transcoding
- `make test`: locked npm install, TypeScript, production web build, all 180 frontend tests in 22 files, Go vet, full Go tests
- `go test -json -race -count=1 -coverprofile=coverage.out -timeout=600s ./...`: 687 test/subtest passes, no failures, across 21 packages containing tests; three additional packages contain no tests
- Go statement coverage: 66.1% overall. Coverage is execution evidence, not proof of correctness; entrypoint/benchmark paths are especially incomplete
- Final migration and watcher follow-up: the full race/coverage suite was rerun after all migration and watcher-regression changes; empty down/up, populated original-schema upgrade, and supported-data-preserving downgrade include foreign-key/integrity checks
- `go vet ./...`, `gofmt -l cmd internal`, and `git diff --check`
- `make -j2 release VERSION=review-fbf2f20-followthrough`: all four Linux/Darwin amd64/arm64 archives compile; archive SHA256SUMS verify; native Linux `--version` reports `review-fbf2f20-followthrough`. Cross-target runtime behavior is not inferred from compilation
- Full and incremental patch reconstruction checks are recorded in the bundle's `patch-validation.txt`

## Explicit skips and limits

- `internal/api/jellyfin.TestCorpus`: no corpus fixtures
- `internal/store.TestSnapshotPublishesAfterCovers`: filesystem accepted the odd failure-injection layout, so the test skipped. Other snapshot/backup tests passed
- ABS corpus contains a single ping fixture. Compatibility unit/HTTP suites do not establish real ABS/Jellyfin/OPDS/Subsonic client acceptance
- Browser launch was previously restricted; no bypass or browser relaunch was attempted. jsdom media mocks cannot verify codecs, autoplay, native PDF, real iframe behavior, device UI or browser memory
- No production data, real library week, live foreign database, real remote feed/provider, hardware encoder, real unrar/unar implementation or Docker runtime was exercised. CBR lifecycle tests use synthetic extractors
- The focused authentication/security review was excluded by the existing platform restriction. Running ordinary existing tests does not establish independent security coverage
- Existing runtime dependency-audit findings remain open; dependency upgrades were not forced
- Cross-built archives were compiled, not run on ARM or macOS. The shipped container recipe was inspected and its frontend commands exercised locally, but the container was not built here
- Release binaries are validation artifacts, not published deliverables. The final source was also checked through the reconstructed checkout's complete `make test` gate; Go test caching is visible in that log, while the separate full race run used `-count=1`

## Run provenance and recovered failures

- The earlier archive-only reconstruction passed tests but its concurrent release attempt failed because VCS metadata was unavailable. `reconstructed-release-initial-failure.log` preserves that failure. Repeating the build in a normal Git-backed checkout succeeded without disabling VCS stamping
- A default npm cache-path failure is preserved in `setup-npm-cache-initial-failure.log`. Final installs used the existing workspace cache in offline mode; the lockfile and integrity checks remained in force
- The first final reconstruction exposed a pre-existing runtime-library test fixture race: scanning could start while its CBZ was unfinished. `reconstructed-watcher-initial-failure.log` records it. The fixture now registers a completed archive and verifies watches are armed. A separate regression forces partial-write failure, finishes the same file, and requires eventual successful ingestion, one present file, and clean shutdown. Both watcher regressions passed 20 race-enabled repetitions; all final aggregate and race gates then passed
- Current authoritative evidence: `reliability-make-test-final.log`, `reliability-race-initial.jsonl` (the final backend source despite the initial filename), `reliability-coverage-initial.out`, `reliability-release-build.log`, `reliability-release-verification.log`, `reliability-reconstructed-test.log` and `validation-summary.json`. The two earlier watcher repetition logs remain under `previous-checkpoint`

## Reliability follow-through evidence

- ABS/Kavita destination rollback: 38 leaf cases fail against the exact preceding importer source via a local Go overlay, then pass after repair. Stages, existing metadata/progress revision/tombstones, deferred commit failure, file replacement, read-only source, dry runs and repeated/single-connection/concurrent imports are covered
- Primary audio: five initial regressions fail before repair. Five further independent reviewer cases exposed chapter-write ordering, explicit and automatic completed-track replay, local edition revisit, and completion during replacement loading; all pass after repair. Originating-account, request body deadline and explicit restart controls are included
- Multipart playlist audio: four tests fail on the preceding first-file-only implementation, then pass with second-file resume, bidirectional cross-file seek, last-file completion and revisit/detached-event checks
- CBR: eight baseline leaf cases fail for cancellation, deadlines, failed partial output and cancellation immediately before publication. Final-state scan race suite and twenty focused repetitions pass; real extractor/resource acceptance remains open
- Independent source review found no escaped destination writes or nested transaction boundary in either importer
- The current full backend race run passes 687 test/subtest cases with 66.1% statement coverage and the same two explicit skips. All 180 frontend tests pass in 22 files, with TypeScript and the production build
- Raw current evidence is under validation/reliability-*; earlier checkpoint logs remain in validation/previous-checkpoint and retain their original counts and context

## Before/after evidence

- Eight prior reader-hook regressions failed against the untouched baseline
- Prior follow-on suite: 18 of 20 initially failed, two compatibility controls passed
- New reader UI comparison: 18 of 21 failed on baseline, three controls passed; final reader-focused suite contains 36 tests
- Search: six initial regressions failed on baseline; seven pass after repair
- Secondary audio: six baseline failures plus four independently reproduced session/revisit/route defects; all pass after repair, alongside three HTTP-error regressions
- ABS conversion, import file-batch rollback, tagged audio order, cancellation and legacy resume repairs each include synthetic regressions. New failures were verified before the corresponding fixes as described in the issue register
- Watcher partial-write recovery and runtime library discovery each passed 20 race-enabled repetitions after the fixture correction
- Games downgrade produced dangling work/playlist references; independent review additionally reproduced dangling scan_jobs. Final fixture covers all three families and preserves ordinary book data

See `systematic-issue-register.md` for each repaired issue and remaining acceptance gate, and `media-consistency-proposal.md` for the remaining implementation contracts and safe next steps. Logs in the bundle use the execution machine's local timestamps; this report date is UTC.
