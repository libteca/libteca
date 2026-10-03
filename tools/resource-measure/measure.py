import argparse
import collections
from datetime import datetime, timezone
import hashlib
import json
import os
from pathlib import Path
import platform
import random
import shutil
import struct
import subprocess
import sys
import tempfile
import time
import zipfile
import zlib


CASES = ("cbz-flat", "cbz-noise", "cbz-large-pixels", "zip-expansion", "zip-directory", "epub-text")
CHUNK_BYTES = 65536
STAGE_TIMEOUT_SECONDS = 45
SAMPLE_INTERVAL_SECONDS = 0.02
MAX_GENERATED_ENTRIES = 4096
MAX_GENERATED_EXPANDED_BYTES = 32 * 1024 * 1024
PNG_SIGNATURE = b"\x89PNG\r\n\x1a\n"


def png_chunk(kind, data):
    return struct.pack(">I", len(data)) + kind + data + struct.pack(">I", zlib.crc32(kind + data))


def png_bytes(width, height, seed, noise=False):
    compressor = zlib.compressobj(6)
    rng = random.Random(seed)
    chunks = []
    flat_row = bytes((seed % 256, 80, 160)) * width
    for _ in range(height):
        row = rng.randbytes(width * 3) if noise else flat_row
        chunks.append(compressor.compress(b"\0" + row))
    chunks.append(compressor.flush())
    header = struct.pack(">IIBBBBB", width, height, 8, 2, 0, 0, 0)
    return PNG_SIGNATURE + png_chunk(b"IHDR", header) + png_chunk(b"IDAT", b"".join(chunks)) + png_chunk(b"IEND", b"")


def zip_entry(name, compressed=True):
    info = zipfile.ZipInfo(name, date_time=(2000, 1, 1, 0, 0, 0))
    info.compress_type = zipfile.ZIP_DEFLATED if compressed else zipfile.ZIP_STORED
    info.external_attr = 0o100600 << 16
    return info


def create_archive(case, directory):
    target = directory / "fixture.zip"
    with zipfile.ZipFile(target, "x", compression=zipfile.ZIP_DEFLATED, compresslevel=6) as archive:
        if case.startswith("cbz-"):
            count, width, height = {
                "cbz-flat": (12, 800, 1200),
                "cbz-noise": (6, 640, 960),
                "cbz-large-pixels": (1, 4096, 4096),
            }[case]
            for index in range(count):
                archive.writestr(zip_entry(f"page-{index:04d}.png"), png_bytes(width, height, index + 1, case == "cbz-noise"))
        elif case == "zip-expansion":
            with archive.open(zip_entry("repeated.bin"), "w") as output:
                for _ in range(128):
                    output.write(b"\0" * CHUNK_BYTES)
        elif case == "zip-directory":
            for index in range(2048):
                archive.writestr(zip_entry(f"entries/{index:04d}.txt"), b"")
        elif case == "epub-text":
            archive.writestr(zip_entry("mimetype", False), b"application/epub+zip")
            archive.writestr(zip_entry("META-INF/container.xml"), '<container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container"><rootfiles><rootfile full-path="OEBPS/package.opf" media-type="application/oebps-package+xml"/></rootfiles></container>')
            items = '<item id="nav" href="nav.xhtml" media-type="application/xhtml+xml" properties="nav"/><item id="style" href="style.css" media-type="text/css"/>'
            spine = ""
            nav = ""
            for index in range(16):
                name = f"chapter-{index:02d}.xhtml"
                body = '<p>A generated paragraph for bounded extraction measurements.</p>' * 1024
                archive.writestr(zip_entry(f"OEBPS/{name}"), '<html xmlns="http://www.w3.org/1999/xhtml"><head><title>Generated chapter</title><link rel="stylesheet" href="style.css"/></head><body>' + body + '</body></html>')
                items += f'<item id="c{index}" href="{name}" media-type="application/xhtml+xml"/>'
                spine += f'<itemref idref="c{index}"/>'
                nav += f'<li><a href="{name}">Chapter {index + 1}</a></li>'
            package = '<package xmlns="http://www.idpf.org/2007/opf" version="3.0" unique-identifier="id"><metadata xmlns:dc="http://purl.org/dc/elements/1.1/"><dc:identifier id="id">urn:libteca:generated:resource-measure</dc:identifier><dc:title>Generated measurement fixture</dc:title><dc:language>en</dc:language><meta property="dcterms:modified">2000-01-01T00:00:00Z</meta></metadata><manifest>' + items + '</manifest><spine>' + spine + '</spine></package>'
            archive.writestr(zip_entry("OEBPS/package.opf"), package)
            archive.writestr(zip_entry("OEBPS/nav.xhtml"), '<html xmlns="http://www.w3.org/1999/xhtml" xmlns:epub="http://www.idpf.org/2007/ops"><head><title>Contents</title></head><body><nav epub:type="toc"><ol>' + nav + '</ol></nav></body></html>')
            archive.writestr(zip_entry("OEBPS/style.css"), "body { font-family: serif; } p { line-height: 1.4; }")
        else:
            raise ValueError("unknown generated fixture")
    return {"case": case, "archive_bytes": target.stat().st_size, "archive_sha256": file_digest(target)}


def file_digest(path):
    digest = hashlib.sha256()
    with path.open("rb") as source:
        for chunk in iter(lambda: source.read(CHUNK_BYTES), b""):
            digest.update(chunk)
    return digest.hexdigest()


def disk_usage(root):
    logical = allocated = count = 0
    for directory, _, names in os.walk(root):
        for name in names:
            path = Path(directory) / name
            try:
                info = path.lstat()
            except FileNotFoundError:
                continue
            if path.is_symlink() or not path.is_file():
                continue
            logical += info.st_size
            allocated += info.st_blocks * 512
            count += 1
    return {"logical_file_bytes": logical, "allocated_file_bytes": allocated, "regular_files": count}


def observe_peak(peak, current):
    for key, value in current.items():
        peak[key] = max(peak.get(key, 0), value)


def png_rgba_estimate(prefix):
    if len(prefix) < 24 or prefix[:8] != PNG_SIGNATURE or prefix[12:16] != b"IHDR":
        return 0, 0
    width, height = struct.unpack(">II", prefix[16:24])
    pixels = width * height
    return pixels, pixels * 4


def measure_archive(directory):
    path = directory / "fixture.zip"
    output_dir = directory / "extracted"
    output_dir.mkdir()
    total = largest = image_count = max_pixels = decoded_total = decoded_window_peak = blob_window_peak = 0
    image_blobs = collections.deque(maxlen=3)
    image_decodes = collections.deque(maxlen=3)
    started = time.monotonic()
    with zipfile.ZipFile(path) as archive:
        listing_seconds = time.monotonic() - started
        entries = archive.infolist()
        declared_total = sum(entry.file_size for entry in entries)
        if len(entries) > MAX_GENERATED_ENTRIES or declared_total > MAX_GENERATED_EXPANDED_BYTES:
            raise ValueError("input exceeds fixed generated-fixture envelope")
        extract_started = time.monotonic()
        for index, entry in enumerate(entries):
            size = 0
            prefix = bytearray()
            destination = output_dir / f"entry-{index:04d}.bin"
            with archive.open(entry) as source, destination.open("xb") as output:
                while chunk := source.read(CHUNK_BYTES):
                    if len(chunk) > MAX_GENERATED_EXPANDED_BYTES - total:
                        raise ValueError("actual bytes exceed fixed generated-fixture envelope")
                    output.write(chunk)
                    prefix.extend(chunk[:max(0, 24 - len(prefix))])
                    size += len(chunk)
                    total += len(chunk)
            if size != entry.file_size:
                raise ValueError("declared and extracted sizes differ")
            largest = max(largest, size)
            pixels, decoded = png_rgba_estimate(prefix)
            if pixels:
                image_count += 1
                max_pixels = max(max_pixels, pixels)
                decoded_total += decoded
                image_blobs.append(size)
                image_decodes.append(decoded)
                blob_window_peak = max(blob_window_peak, sum(image_blobs))
                decoded_window_peak = max(decoded_window_peak, sum(image_decodes))
        extract_seconds = time.monotonic() - extract_started
        compressed_payload = sum(entry.compress_size for entry in entries)
        directory_bytes = sum(46 + len(entry.filename.encode("utf-8")) + len(entry.extra) + len(entry.comment) for entry in entries)
    disk = disk_usage(output_dir)
    return {
        "archive_bytes": path.stat().st_size,
        "archive_sha256": file_digest(path),
        "zip_compressed_payload_bytes": compressed_payload,
        "zip_central_directory_bytes": directory_bytes,
        "zip_entry_count": len(entries),
        "declared_extracted_bytes": declared_total,
        "actual_extracted_bytes": total,
        "largest_extracted_entry_bytes": largest,
        "retained_extracted_disk": disk,
        "image_count": image_count,
        "largest_image_pixels": max_pixels,
        "decoded_rgba_all_images_estimate_bytes": decoded_total,
        "modeled_three_image_blob_retention_peak_bytes": blob_window_peak,
        "modeled_three_image_rgba_retention_peak_bytes": decoded_window_peak,
        "zip_listing_seconds": listing_seconds,
        "streamed_extraction_seconds": extract_seconds,
        "extraction_to_archive_ratio": total / path.stat().st_size,
    }


def rss_bytes(usage):
    return int(usage.ru_maxrss) * (1 if sys.platform == "darwin" else 1024)


def sampled_child_rss(pid):
    if sys.platform != "linux":
        return None
    try:
        for line in Path(f"/proc/{pid}/status").read_text().splitlines():
            if line.startswith("VmRSS:"):
                return int(line.split()[1]) * 1024
    except (FileNotFoundError, ProcessLookupError):
        pass
    return 0


def run_group(commands, root, label, timeout=STAGE_TIMEOUT_SECONDS):
    if not hasattr(os, "wait4"):
        raise RuntimeError("measurement needs POSIX wait4 for per-child peak RSS")
    log_dir = root / "logs"
    log_dir.mkdir(exist_ok=True)
    jobs = []
    group_started = time.monotonic()
    peak_disk = disk_usage(root)
    samples = 0
    sampled_aggregate_rss_peak = 0 if sys.platform == "linux" else None
    sampled_live_children_peak = 0 if sys.platform == "linux" else None
    try:
        for index, command in enumerate(commands):
            stdout_path = log_dir / f"{label}-{index}.out"
            stderr_path = log_dir / f"{label}-{index}.err"
            with stdout_path.open("xb") as stdout, stderr_path.open("xb") as stderr:
                process = subprocess.Popen(command, stdin=subprocess.DEVNULL, stdout=stdout, stderr=stderr, cwd=root)
            jobs.append({"process": process, "started": time.monotonic(), "stdout": stdout_path, "stderr": stderr_path, "metrics": None})
        pending = len(jobs)
        while pending:
            observe_peak(peak_disk, disk_usage(root))
            samples += 1
            if sys.platform == "linux":
                live_rss = [sampled_child_rss(job["process"].pid) for job in jobs if job["metrics"] is None]
                sampled_aggregate_rss_peak = max(sampled_aggregate_rss_peak, sum(live_rss))
                sampled_live_children_peak = max(sampled_live_children_peak, sum(value > 0 for value in live_rss))
            for job in jobs:
                if job["metrics"] is not None:
                    continue
                process = job["process"]
                timed_out = time.monotonic() - job["started"] >= timeout
                if timed_out:
                    process.kill()
                pid, status, usage = os.wait4(process.pid, 0 if timed_out else os.WNOHANG)
                if not pid:
                    continue
                process.returncode = os.waitstatus_to_exitcode(status)
                job["metrics"] = {
                    "exit_code": process.returncode,
                    "timed_out": timed_out,
                    "wall_seconds": time.monotonic() - job["started"],
                    "child_peak_rss_bytes": rss_bytes(usage),
                    "child_user_cpu_seconds": usage.ru_utime,
                    "child_system_cpu_seconds": usage.ru_stime,
                }
                pending -= 1
            if pending:
                time.sleep(SAMPLE_INTERVAL_SECONDS)
    finally:
        for job in jobs:
            process = job["process"]
            if process.returncode is None:
                process.kill()
                _, status, _ = os.wait4(process.pid, 0)
                process.returncode = os.waitstatus_to_exitcode(status)
    observe_peak(peak_disk, disk_usage(root))
    return {
        "concurrent_jobs_requested": len(commands),
        "wall_seconds": time.monotonic() - group_started,
        "disk_samples": samples,
        "sampled_aggregate_child_rss_peak_bytes": sampled_aggregate_rss_peak,
        "sampled_live_children_peak": sampled_live_children_peak,
        "sampled_peak_workspace_disk": peak_disk,
        "final_workspace_disk": disk_usage(root),
        "jobs": [dict(job["metrics"], stdout=job["stdout"].relative_to(root).as_posix(), stderr=job["stderr"].relative_to(root).as_posix()) for job in jobs],
    }


def require_success(stage, root):
    for job in stage["jobs"]:
        if job["exit_code"] or job["timed_out"]:
            detail = (root / job["stderr"]).read_text(errors="replace")[:2048]
            raise RuntimeError(f"measurement subprocess failed: {job['exit_code']}, timeout={job['timed_out']}: {detail}")


def json_output(stage, root):
    require_success(stage, root)
    return json.loads((root / stage["jobs"][0]["stdout"]).read_text())


def ffmpeg_prefix(executable):
    return [executable, "-nostdin", "-hide_banner", "-loglevel", "error", "-threads", "1", "-filter_threads", "1"]


def hls_command(executable, source, directory, start):
    return ffmpeg_prefix(executable) + ["-ss", str(start), "-i", str(source), "-t", str(12 - start), "-an", "-c:v", "libx264", "-threads", "1", "-preset", "veryfast", "-crf", "23", "-g", "48", "-keyint_min", "48", "-sc_threshold", "0", "-f", "hls", "-hls_time", "2", "-hls_list_size", "0", "-hls_segment_filename", str(directory / "segment-%03d.ts"), str(directory / "index.m3u8")]


def run_media(root, executable):
    media = root / "media"
    media.mkdir()
    source = media / "generated.mp4"
    generate = ffmpeg_prefix(executable) + ["-f", "lavfi", "-i", "testsrc2=size=640x360:rate=24", "-t", "12", "-an", "-c:v", "libx264", "-threads", "1", "-preset", "veryfast", "-crf", "23", str(source)]
    generation = run_group([generate], root, "media-generation")
    require_success(generation, root)
    results = {"status": "measured", "source": {"generator": "ffmpeg lavfi testsrc2", "width": 640, "height": 360, "fps": 24, "duration_seconds": 12, "audio_streams": 0, "encoded_bytes": source.stat().st_size, "sha256": file_digest(source)}, "generation": generation, "hls_sessions": []}
    sessions = []
    for name in ("initial-a", "initial-b", "seek-4", "seek-8"):
        path = media / name
        path.mkdir()
        sessions.append(path)
    waves = [([0, 1], [0, 0]), ([2], [4]), ([3], [8])]
    for wave, (indices, starts) in enumerate(waves):
        commands = [hls_command(executable, source, sessions[index], start) for index, start in zip(indices, starts)]
        measurement = run_group(commands, root, f"hls-{wave}")
        require_success(measurement, root)
        for index, start, job in zip(indices, starts, measurement["jobs"]):
            playlist = (sessions[index] / "index.m3u8").read_text()
            segments = list(sessions[index].glob("*.ts"))
            if not segments or "#EXT-X-ENDLIST" not in playlist:
                raise RuntimeError("generated HLS session did not finish")
            results["hls_sessions"].append({"name": sessions[index].name, "start_seconds": start, "segment_count": len(segments), "segment_bytes": sum(path.stat().st_size for path in segments), "retained_output_disk": disk_usage(sessions[index]), "measurement": job})
        results.setdefault("hls_waves", []).append(measurement)
    results["hls_retained_all_sessions_disk"] = {key: sum(disk_usage(path)[key] for path in sessions) for key in disk_usage(sessions[0])}
    results["trickplay"] = []
    for width in (160, 320):
        directory = media / f"trickplay-{width}"
        directory.mkdir()
        command = ffmpeg_prefix(executable) + ["-i", str(source), "-vf", f"fps=1/10,scale={width}:-2,tile=10x10", "-q:v", "4", "-threads", "1", "-start_number", "0", str(directory / "%d.jpg")]
        measurement = run_group([command], root, f"trickplay-{width}")
        require_success(measurement, root)
        sheets = list(directory.glob("*.jpg"))
        if not sheets:
            raise RuntimeError("generated trickplay produced no sheets")
        results["trickplay"].append({"width": width, "frame_height": width * 360 // 640, "sample_interval_seconds": 10, "tile_columns": 10, "tile_rows": 10, "sheet_count": len(sheets), "retained_output_disk": disk_usage(directory), "one_sheet_rgba_estimate_bytes": width * (width * 360 // 640) * 100 * 4, "measurement": measurement})
    decoded = root / "archives" / "cbz-large-pixels" / "extracted" / "entry-0000.bin"
    command = ffmpeg_prefix(executable) + ["-i", str(decoded), "-frames:v", "1", "-threads", "1", "-f", "null", "-"]
    results["large_png_native_decode"] = run_group([command], root, "native-png-decode")
    require_success(results["large_png_native_decode"], root)
    return results


def version_line(executable):
    result = subprocess.run([executable, "-version"], stdin=subprocess.DEVNULL, capture_output=True, text=True, timeout=5, check=True)
    return result.stdout.splitlines()[0]


def run_suite(work_parent, include_media=True):
    script = Path(__file__).resolve()
    executable = shutil.which("ffmpeg") if include_media else None
    report = {
        "schema_version": 1,
        "measured_at_utc": datetime.now(timezone.utc).isoformat(),
        "scope": "generated disposable fixtures; no libteca runtime integration or production cap selection",
        "environment": {"python": platform.python_version(), "os": platform.system(), "architecture": platform.machine(), "ffmpeg": version_line(executable) if executable else None},
        "harness": {"script_sha256": file_digest(script), "sample_interval_seconds": SAMPLE_INTERVAL_SECONDS, "stage_timeout_seconds": STAGE_TIMEOUT_SECONDS, "archive_extraction_chunk_bytes": CHUNK_BYTES, "max_generated_entries": MAX_GENERATED_ENTRIES, "max_generated_expanded_bytes": MAX_GENERATED_EXPANDED_BYTES, "max_concurrent_media_jobs": 2},
        "archives": [],
    }
    started = time.monotonic()
    temporary = tempfile.TemporaryDirectory(prefix="libteca-resource-generated-", dir=work_parent)
    root = Path(temporary.name)
    try:
        (root / "archives").mkdir()
        for case in CASES:
            print(f"measuring {case}", file=sys.stderr, flush=True)
            directory = root / "archives" / case
            directory.mkdir()
            common = [sys.executable, "-B", str(script), "--worker", "--case", case, "--directory", str(directory)]
            generation = run_group([common + ["--operation", "generate"]], root, case + "-generation")
            generated = json_output(generation, root)
            extraction = run_group([common + ["--operation", "extract"]], root, case + "-extraction")
            measured = json_output(extraction, root)
            if generated["archive_sha256"] != measured["archive_sha256"]:
                raise RuntimeError("generated archive changed during measurement")
            report["archives"].append({"case": case, "generation": generation, "extraction": extraction, "metrics": measured})
        if executable:
            print("measuring generated native media", file=sys.stderr, flush=True)
            report["media"] = run_media(root, executable)
        else:
            report["media"] = {"status": "skipped", "reason": "disabled by --archives-only" if not include_media else "ffmpeg unavailable"}
        report["final_workspace_disk"] = disk_usage(root)
        report["status"] = "measured" if executable else "archives-measured-media-skipped"
    finally:
        temporary.cleanup()
        report["cleanup_verified"] = not root.exists()
    report["wall_seconds"] = time.monotonic() - started
    return report


def main():
    parser = argparse.ArgumentParser(description="Measure fixed generated archives and optional media in a disposable child directory")
    parser.add_argument("--work-parent", type=Path)
    parser.add_argument("--archives-only", action="store_true")
    parser.add_argument("--worker", action="store_true", help=argparse.SUPPRESS)
    parser.add_argument("--case", choices=CASES, help=argparse.SUPPRESS)
    parser.add_argument("--directory", type=Path, help=argparse.SUPPRESS)
    parser.add_argument("--operation", choices=("generate", "extract"), help=argparse.SUPPRESS)
    args = parser.parse_args()
    if args.worker:
        if not args.case or not args.directory or not args.operation:
            parser.error("internal worker requires case, directory and operation")
        result = create_archive(args.case, args.directory) if args.operation == "generate" else measure_archive(args.directory)
    else:
        if args.work_parent is None or not args.work_parent.is_dir():
            parser.error("--work-parent must name an existing disposable-work parent directory")
        result = run_suite(args.work_parent.resolve(), not args.archives_only)
    print(json.dumps(result, indent=2, sort_keys=True))


if __name__ == "__main__":
    main()
