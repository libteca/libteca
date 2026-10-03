import importlib.util
import json
import os
from pathlib import Path
import struct
import sys
import tempfile
import unittest
from unittest import mock
import zipfile
import zlib


SPEC = importlib.util.spec_from_file_location("measure", Path(__file__).with_name("measure.py"))
MEASURE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(MEASURE)


class ArchiveTests(unittest.TestCase):
    def test_png_is_valid_and_deterministic(self):
        for noise in (False, True):
            data = MEASURE.png_bytes(7, 5, 3, noise)
            self.assertEqual(data, MEASURE.png_bytes(7, 5, 3, noise))
            self.assertEqual(MEASURE.png_rgba_estimate(data), (35, 140))
            cursor = 8
            compressed = b""
            while cursor < len(data):
                size = struct.unpack(">I", data[cursor:cursor + 4])[0]
                kind = data[cursor + 4:cursor + 8]
                payload = data[cursor + 8:cursor + 8 + size]
                crc = struct.unpack(">I", data[cursor + 8 + size:cursor + 12 + size])[0]
                self.assertEqual(crc, zlib.crc32(kind + payload))
                if kind == b"IDAT":
                    compressed += payload
                cursor += 12 + size
            self.assertEqual(len(zlib.decompress(compressed)), (1 + 7 * 3) * 5)

    def test_compressed_extracted_retained_and_decoded_differ(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            MEASURE.create_archive("cbz-flat", root)
            result = MEASURE.measure_archive(root)
            self.assertEqual(result["image_count"], 12)
            self.assertEqual(result["zip_entry_count"], 12)
            self.assertEqual(result["declared_extracted_bytes"], result["actual_extracted_bytes"])
            self.assertEqual(result["actual_extracted_bytes"], result["retained_extracted_disk"]["logical_file_bytes"])
            self.assertEqual(result["decoded_rgba_all_images_estimate_bytes"], 12 * 800 * 1200 * 4)
            self.assertEqual(result["modeled_three_image_rgba_retention_peak_bytes"], 3 * 800 * 1200 * 4)
            self.assertGreater(result["decoded_rgba_all_images_estimate_bytes"], result["actual_extracted_bytes"])
            self.assertLess(result["modeled_three_image_blob_retention_peak_bytes"], result["actual_extracted_bytes"])

    def test_directory_cost_exists_with_zero_extracted_bytes(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            MEASURE.create_archive("zip-directory", root)
            result = MEASURE.measure_archive(root)
            self.assertEqual(result["zip_entry_count"], 2048)
            self.assertEqual(result["actual_extracted_bytes"], 0)
            self.assertEqual(result["retained_extracted_disk"]["regular_files"], 2048)
            self.assertGreater(result["zip_central_directory_bytes"], 100000)

    def test_epub_structure_and_extraction(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            MEASURE.create_archive("epub-text", root)
            with zipfile.ZipFile(root / "fixture.zip") as archive:
                self.assertEqual(archive.infolist()[0].filename, "mimetype")
                self.assertEqual(archive.infolist()[0].compress_type, zipfile.ZIP_STORED)
                self.assertEqual(archive.read("mimetype"), b"application/epub+zip")
                self.assertIn(b"chapter-15.xhtml", archive.read("OEBPS/package.opf"))
            result = MEASURE.measure_archive(root)
            self.assertEqual(result["zip_entry_count"], 21)
            self.assertGreater(result["actual_extracted_bytes"], 1024 * 1024)
            self.assertEqual(result["image_count"], 0)

    def test_generated_envelope_minus_exact_and_plus_one(self):
        for size in (3, 4, 5):
            with self.subTest(size=size), tempfile.TemporaryDirectory() as temporary:
                root = Path(temporary)
                with zipfile.ZipFile(root / "fixture.zip", "w") as archive:
                    archive.writestr("entry", b"x" * size)
                with mock.patch.object(MEASURE, "MAX_GENERATED_EXPANDED_BYTES", 4):
                    if size > 4:
                        with self.assertRaisesRegex(ValueError, "envelope"):
                            MEASURE.measure_archive(root)
                        self.assertEqual(list((root / "extracted").iterdir()), [])
                    else:
                        self.assertEqual(MEASURE.measure_archive(root)["actual_extracted_bytes"], size)

    def test_non_png_has_no_decoded_estimate(self):
        self.assertEqual(MEASURE.png_rgba_estimate(b"not an image"), (0, 0))


@unittest.skipUnless(hasattr(os, "wait4"), "POSIX wait4 required")
class SupervisorTests(unittest.TestCase):
    def test_disk_final_state_and_per_child_rss(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            commands = [[sys.executable, "-c", f"from pathlib import Path; Path('generated-{i}').write_bytes(b'x' * 8192)"] for i in range(2)]
            result = MEASURE.run_group(commands, root, "parallel", timeout=5)
            MEASURE.require_success(result, root)
            self.assertEqual(result["concurrent_jobs_requested"], 2)
            self.assertGreaterEqual(result["final_workspace_disk"]["logical_file_bytes"], 16384)
            self.assertGreaterEqual(result["sampled_peak_workspace_disk"]["logical_file_bytes"], result["final_workspace_disk"]["logical_file_bytes"])
            if sys.platform == "linux":
                self.assertIsNotNone(result["sampled_aggregate_child_rss_peak_bytes"])
            for job in result["jobs"]:
                self.assertGreater(job["child_peak_rss_bytes"], 0)
                self.assertGreater(job["wall_seconds"], 0)

    def test_timeout_kills_and_reaps_launched_child(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            command = [sys.executable, "-c", "import os, time; print(os.getpid(), flush=True); time.sleep(30)"]
            result = MEASURE.run_group([command], root, "timeout", timeout=0.15)
            job = result["jobs"][0]
            self.assertTrue(job["timed_out"])
            self.assertNotEqual(job["exit_code"], 0)
            pid = int((root / job["stdout"]).read_text())
            with self.assertRaises(ChildProcessError):
                os.waitpid(pid, os.WNOHANG)
            with self.assertRaisesRegex(RuntimeError, "subprocess failed"):
                MEASURE.require_success(result, root)

    def test_interrupt_reaps_launched_child(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            command = [sys.executable, "-c", "import time; time.sleep(30)"]
            actual_popen = MEASURE.subprocess.Popen
            launched = []
            def launch(*args, **kwargs):
                process = actual_popen(*args, **kwargs)
                launched.append(process)
                return process
            with mock.patch.object(MEASURE.subprocess, "Popen", side_effect=launch), mock.patch.object(MEASURE.time, "sleep", side_effect=KeyboardInterrupt):
                with self.assertRaises(KeyboardInterrupt):
                    MEASURE.run_group([command], root, "interrupt", timeout=5)
            self.assertIsNotNone(launched[0].returncode)
            with self.assertRaises(ChildProcessError):
                os.waitpid(launched[0].pid, os.WNOHANG)

    def test_suite_failure_cleans_only_generated_directory(self):
        with tempfile.TemporaryDirectory() as temporary:
            parent = Path(temporary)
            sentinel = parent / "existing"
            sentinel.write_text("preserve")
            with mock.patch.object(MEASURE, "run_group", side_effect=RuntimeError("forced failure")):
                with self.assertRaisesRegex(RuntimeError, "forced failure"):
                    MEASURE.run_suite(parent, include_media=False)
            self.assertEqual(list(parent.iterdir()), [sentinel])
            self.assertEqual(sentinel.read_text(), "preserve")

    def test_suite_reports_media_skip_and_verified_cleanup(self):
        with tempfile.TemporaryDirectory() as temporary:
            parent = Path(temporary)
            with mock.patch.object(MEASURE, "CASES", ("zip-expansion",)):
                result = MEASURE.run_suite(parent, include_media=False)
            self.assertEqual(result["status"], "archives-measured-media-skipped")
            self.assertEqual(result["media"]["status"], "skipped")
            self.assertTrue(result["cleanup_verified"])
            self.assertEqual(list(parent.iterdir()), [])
            self.assertEqual(result["archives"][0]["metrics"]["actual_extracted_bytes"], 8 * 1024 * 1024)
            json.dumps(result)


if __name__ == "__main__":
    unittest.main()
