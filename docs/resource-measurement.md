# Disposable resource measurements

Status: measurement preparation only. This does not choose or enforce production limits, integrate a new libteca runtime path, or close the archive, CBR, HLS, browser, or real-library acceptance gates in `AUDIT_OPEN.md` and `media-consistency-proposal.md`.

## Run it

Python 3.10+ and POSIX `wait4` are required. Python's standard library performs the archive work. An already-installed ffmpeg with `libx264` performs the optional native media work. No package installation, network, database, existing media, or server is used.

From the repository root:

```sh
mkdir -p tools/resource-measure/.runs
python3 -B -m unittest discover -s tools/resource-measure -p 'test_*.py' -v
python3 -B tools/resource-measure/measure.py \
  --work-parent tools/resource-measure/.runs > /tmp/libteca-resource-measurement.json
```

Use `--archives-only` to omit native media intentionally. Missing ffmpeg produces an explicit media-skipped result; ffmpeg present but failing a requested operation fails the run. Archive-only success must not be reported as a native media pass. No hardware encoder is selected.

The command creates its own private, uniquely named `libteca-resource-generated-*` child directory below the supplied parent. It never scans or modifies the parent's existing contents. Fixtures, extracted files, native outputs and process logs live exclusively in that generated child. Normal completion and Python exception unwinding remove only that child; the report includes `cleanup_verified`. The parent and the redirected JSON report remain. A forcibly terminated interpreter or machine can leave the generated child behind; the harness does not claim crash-proof cleanup or persistent process supervision.

The checked-in example is `tools/resource-measure/evidence/local-synthetic.json`. It records the measured script SHA-256, generated archive hashes, environment, run time, phase metrics and cleanup result. Its relative log paths describe disposable files that were removed; it is not a manifest of deliverable media. Timing, process RSS and allocated storage vary between runs. Generated archive bytes are deterministic under the recorded Python/zlib behavior; encoded media can vary between ffmpeg builds.

## Bounded generated corpus

All counts, dimensions and durations are fixed in the harness. There is no external-input option or scale multiplier in the public CLI.

| Fixture | Generated content | Purpose |
|---|---|---|
| `cbz-flat` | Twelve 800 by 1200 flat RGB PNG pages | Separates ZIP/PNG compression from pixels and modeled retention |
| `cbz-noise` | Six 640 by 960 seeded-noise RGB PNG pages | Less-compressible pages and multi-page retained bytes |
| `cbz-large-pixels` | One 4096 by 4096 flat RGB PNG | Large decoded estimate with tiny on-disk representation |
| `zip-expansion` | One 8 MiB repeated-byte entry | High ZIP expansion with streamed extraction |
| `zip-directory` | 2,048 zero-byte entries | Directory/file-count cost independent of extracted payload bytes |
| `epub-text` | EPUB packaging, sixteen repeated-text XHTML chapters, navigation and CSS | Dependency byte expansion; no DOM rendering claim |
| Native video | Twelve seconds, 640 by 360, 24 fps `testsrc2`, H.264, no audio | Small, repeatable native encoding workload |

The generated PNG is constructed row by row without allocating a full decoded bitmap. ZIP extraction writes 64 KiB chunks and holds at most a small header sample for image dimensions. The largest generated ZIP expansion is 8 MiB; a fixed 4,096-entry/32 MiB expanded-byte envelope also rejects accidentally changed inputs. That envelope is a harness safeguard, not a proposed CBZ/EPUB/CBR policy. Every measured subprocess has a 45-second wall deadline; at most two native media commands run together. These deadlines and sizes bound the experiment, not ordinary books or long videos. They are not ongoing filesystem quotas. The example retained about 34.3 MiB of logical files before cleanup; allow additional free space for filesystem metadata and temporary allocation.

Media stages create two initial HLS sessions concurrently, then separate seek generations at 4 and 8 seconds while retaining every previous session directory. The HLS recipe uses two-second target segments, all segments in the playlist, H.264 `veryfast`/CRF 23, and one codec/filter thread per process. It deliberately does not delete advertised output or invent a rolling-window policy. These are direct ffmpeg experiments, not calls through `transcode.Manager`; existing runtime session admission, seeking, reaping and publication behavior are not exercised.

Trickplay uses the source's current `fps=1/10`, widths 160 and 320, 10 by 10 tiling and JPEG quality 4, with thread counts restricted for the experiment. It does not call the runtime `Generator`, publish a `COMPLETE` marker, or verify serving/cache-generation semantics. A final native PNG-to-null decode measures an actual decoder process separately from the image-size estimate.

## Metric meanings

- `archive_bytes`: actual ZIP container size, including directory and local-header overhead
- `zip_compressed_payload_bytes`: sum of compressed entry payload sizes
- `zip_central_directory_bytes`: sum of fixed central-directory record bytes plus names, extras and comments for these generated, ASCII-named, non-ZIP64 fixtures; this is serialized directory size, not parser memory
- `declared_extracted_bytes`: total from the ZIP directory
- `actual_extracted_bytes`: bytes actually consumed and written while streaming every entry, checked against declarations; `largest_extracted_entry_bytes` keeps the single-entry dimension separate
- `retained_extracted_disk`: all extracted entry files still present after extraction; retained data is on disk, not retained in a Python or browser image cache
- `decoded_rgba_all_images_estimate_bytes`: image width times height times four, summed over PNGs; no Python image decode is performed
- `modeled_three_image_*_retention_peak_bytes`: maximum sum in a three-image sequential window, once for PNG payload bytes and once for RGBA estimates; a sizing model, not a browser cache implementation or a recommended cache size
- `logical_file_bytes`: sum of regular-file lengths at the observation point
- `allocated_file_bytes`: sum of regular-file `st_blocks * 512`; this excludes directory, inode and filesystem metadata, journal, snapshots, compression/deduplication effects, unrelated files and capacity reserved by the host
- `sampled_peak_workspace_disk`: maxima from periodic whole-generated-workspace observations, including prior stages, source, output and logs; final per-session disk is also recorded separately
- `child_peak_rss_bytes`: kernel `wait4` high-water RSS of that one direct child, converted from KiB on Linux and bytes on macOS; includes interpreter/native-tool overhead, but not the Python supervisor, other concurrent jobs, browser/GPU memory or system page cache
- `sampled_aggregate_child_rss_peak_bytes`: maximum observed sum of simultaneous direct-child `/proc/<pid>/status` RSS on Linux; `sampled_live_children_peak` counts sampled children with positive RSS. Both are null off Linux. Linux process-accounting sources can differ slightly, so these samples need not agree exactly with the sum of `wait4` peaks
- Phase `wall_seconds`: launch-to-observed-completion, including interpreter startup and observation delay; archive `zip_listing_seconds` and `streamed_extraction_seconds` are worker-internal timings without generation. Generation and extraction run in separate processes so generation RSS is not mislabeled extraction RSS
- `child_user_cpu_seconds` and `child_system_cpu_seconds`: CPU time for the direct measured child. `concurrent_jobs_requested` records orchestration, not proof that both children were simultaneously busy

The nominal sampling interval is 20 ms plus directory walking and process observation. Short-lived peaks can be missed, and filesystem traversal is not an atomic snapshot. Observation overhead becomes visible for many tiny files. RSS units are normalized to bytes, not claims of byte-exact attribution. Concurrent shared pages may be counted more than once by RSS. Do not add individual job RSS peaks and present that as an observed simultaneous peak. Stage metrics include neither a server nor a browser. All phase timestamps are monotonic durations.

## Observed local evidence

The checked-in run was captured on 2026-10-03 UTC, Linux x86-64, Python 3.12.14 and ffmpeg 7.1.5. All requested stages succeeded in 6.03 seconds. The generated tree was removed and cleanup verified. Exact raw values are in the JSON; the summary below is rounded only for time/RSS.

| Archive fixture | Container bytes | Entries | Actual extracted bytes | All-image RGBA estimate | Extraction child peak RSS |
|---|---:|---:|---:|---:|---:|
| Flat CBZ | 3,214 | 12 | 76,630 | 46,080,000 | 13.0 MiB |
| Noise CBZ | 11,072,752 | 6 | 11,068,728 | 14,745,600 | 13.2 MiB |
| Large-pixel CBZ | 769 | 1 | 60,203 | 67,108,864 | 13.0 MiB |
| Repeated-byte ZIP | 8,279 | 1 | 8,388,608 | 0 | 13.1 MiB |
| Directory-heavy ZIP | 225,302 | 2,048 | 0 | 0 | 14.1 MiB |
| Text EPUB | 9,967 | 21 | 1,070,669 | 0 | 13.7 MiB |

Zero decoded estimates mean no measured PNGs, not zero possible rendering/DOM memory. The large-pixel case separately decoded with native ffmpeg at 79.8 MiB child peak RSS in 0.234 seconds. Its 64 MiB RGBA estimate and 60,203-byte PNG demonstrate why an extracted byte limit alone cannot choose a decoded-image budget. The 769-byte outer ZIP further separates download bytes from extracted page bytes. Neither estimate nor ffmpeg RSS predicts browser or GPU memory.

For flat CBZ, modeled three-image retention is 19,158 PNG bytes versus 11,520,000 RGBA bytes. For noise CBZ it is 5,534,364 PNG bytes versus 7,372,800 RGBA bytes. Different content changes which budget dominates. The zero-byte directory fixture still needs 126,976 serialized central-directory bytes and creates 2,048 files; payload totals alone do not measure directory/parser/inode work.

| Native output | Retained logical output bytes | Segments/sheets | Child peak RSS | Observed job wall time |
|---|---:|---:|---:|---:|
| HLS initial A | 980,142 | 6 segments | 66.1 MiB | 0.832 s |
| HLS initial B | 980,142 | 6 segments | 66.2 MiB | 0.831 s |
| HLS seek at 4 s | 654,836 | 4 segments | 66.1 MiB | 0.624 s |
| HLS seek at 8 s | 323,326 | 2 segments | 65.9 MiB | 0.443 s |
| Trickplay width 160 | 12,180 | 1 sheet | 44.8 MiB | 0.257 s |
| Trickplay width 320 | 41,952 | 1 sheet | 62.7 MiB | 0.272 s |

The two initial HLS jobs overlapped in Linux RSS samples: two live children and 132.9 MiB sampled aggregate RSS. Retaining all four sessions used 2,938,446 logical bytes/2,998,272 allocated file bytes across 22 files, excluding the source and trickplay. This illustrates duplicate/seek-generation accumulation; it is not a long-title storage projection. The generated source itself was 980,384 bytes. The two trickplay sheet RGBA estimates were 5,760,000 and 23,040,000 bytes, including the full padded 10 by 10 sheet rather than only nonblank samples.

The complete disposable workspace ended at 35,963,938 logical bytes/36,192,256 allocated file bytes across 2,160 regular files, including archive sources, extracted copies, media, and process logs. Its size is distinct from HLS retention, extracted payload, and a real filesystem's total used space.

## Validation and remaining cap-selection work

Eleven standard-library tests cover deterministic valid PNG construction, separate byte/pixel metrics, three-image accounting, many empty entries, EPUB packaging, the generated envelope at minus-one/exact/plus-one, per-child process accounting, timeout kill/reap, interrupted supervision, media-skip reporting, and cleanup without altering a pre-existing sibling. The full recorded run exercises installed ffmpeg HLS, trickplay and PNG decoding. These focused tests do not replace the repository's Go and web checks.

No runtime resource finding is closed by this harness. Before selecting stricter production budgets:

1. Collect a consented, representative disposable corpus of ordinary long and large books, EPUBs, image shapes and codecs. Record distributions and rejected examples, quality effects, supported devices and a deliberate margin. Do not infer percentiles or safe production limits from one generated run.
2. Instrument the actual browser: ZIP parser and dependency extraction, document/DOM growth, blob/cache retention, active image decodes, GPU/backing buffers, and recoverable quota handling. Python ZIP and ffmpeg measurements are not JSZip, epub.js or browser measurements.
3. Define HLS per-session and aggregate disk admission, concurrent-work accounting and seeking/restart semantics. Verify advertised segments remain retrievable under the chosen retention contract. Repeat seeks, session expiry, failed jobs and process/server restart against the actual runtime, including long titles, audio, high resolution, alternate codecs and hardware acceleration.
4. For CBR, measure supported real unrar/unar versions and platforms in disposable trees, with ongoing-output accounting and deterministic child/process-tree termination. This harness has no RAR implementation, invokes no CBR extractor, and establishes no extraction-disk quota or CBR compatibility result.
5. Add runtime cap-minus-one/exact/plus-one, malformed listings, publication-boundary cancellation, low-disk, concurrency and restart acceptance only after each policy is chosen. The tiny harness-envelope boundary regression is not a runtime policy acceptance test. No deliberate host low-disk or exhaustion test was run.

Retain separate outcomes for synthetic instrumentation, native-tool behavior, runtime acceptance and representative-library quality. This preparation supplies measurement vocabulary, repeatable bounded experiments and initial evidence; it intentionally leaves cap values and destructive retention decisions unchosen.
