package transcode

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

// Video hardware accel modes for HLS transcode sessions.
const (
	AccelNone         = "none"
	AccelVideoToolbox = "videotoolbox"
	AccelVAAPI        = "vaapi"
	AccelNVENC        = "nvenc"
	AccelQSV          = "qsv"
)

const (
	envHwAccel          = "LIBTECA_HWACCEL"
	vaapiDevice         = "/dev/dri/renderD128"
	DefaultVideoBitrate = "6M"
)

var reEncoderLine = regexp.MustCompile(`^\s*[AVS][A-Za-z.]{4,11}\s+(\S+)\s+.*\(codec .*\)$`)

type hwProbe struct {
	hwaccels map[string]bool
	encoders map[string]bool
}

func parseHwaccels(out string) map[string]bool {
	methods := map[string]bool{}
	listed := false
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(strings.TrimRight(line, "\r"))
		if !listed {
			listed = strings.HasPrefix(line, "Hardware acceleration methods:")
			continue
		}
		if line == "" || strings.ContainsAny(line, " \t") {
			break
		}
		methods[line] = true
	}
	return methods
}

func parseEncoders(out string) map[string]bool {
	encoders := map[string]bool{}
	for _, line := range strings.Split(out, "\n") {
		if m := reEncoderLine.FindStringSubmatch(strings.TrimRight(line, "\r")); m != nil {
			encoders[m[1]] = true
		}
	}
	return encoders
}

func renderNodeExists() bool {
	_, err := os.Stat(vaapiDevice)
	return err == nil
}

func validAccel(s string) bool {
	switch s {
	case AccelNone, AccelVideoToolbox, AccelVAAPI, AccelNVENC, AccelQSV:
		return true
	}
	return false
}

// ValidAccel reports whether s is an acceptable --hwaccel value ("auto"
// included). Startup validates explicit requests instead of silently
// continuing on another mode.
func ValidAccel(s string) bool {
	return s == "auto" || validAccel(s)
}

// selectAccel picks the accel mode: env override first, then per-OS candidate
// order filtered by probe results.
func selectAccel(goos string, dri bool, p hwProbe, env string) string {
	if env != "" {
		if validAccel(env) {
			return env
		}
		slog.Warn("transcode: ignoring unknown LIBTECA_HWACCEL", "value", env)
	}
	var cands []string
	if goos == "darwin" {
		cands = []string{AccelVideoToolbox}
	} else {
		cands = []string{AccelNVENC, AccelQSV}
		if dri {
			cands = append([]string{AccelVAAPI}, cands...)
		}
	}
	for _, c := range cands {
		if accelAvailable(c, p) {
			return c
		}
	}
	return AccelNone
}

func accelAvailable(accel string, p hwProbe) bool {
	switch accel {
	case AccelVideoToolbox:
		return p.encoders["h264_videotoolbox"]
	case AccelVAAPI:
		return p.hwaccels["vaapi"] && p.encoders["h264_vaapi"]
	case AccelNVENC:
		return p.encoders["h264_nvenc"]
	case AccelQSV:
		return p.encoders["h264_qsv"]
	}
	return false
}

// buildArgs assembles the ffmpeg HLS command for an accel mode. Audio and
// HLS sections are identical across modes.
func buildArgs(accel, source, dir string, startSecs float64, bitrate string) []string {
	if bitrate == "" {
		bitrate = DefaultVideoBitrate
	}
	args := []string{"-y", "-v", "quiet"}
	if startSecs > 1 {
		args = append(args, "-ss", fmt.Sprintf("%.2f", startSecs))
	}
	switch accel {
	case AccelVAAPI:
		args = append(args, "-hwaccel", "vaapi", "-hwaccel_output_format", "vaapi", "-vaapi_device", vaapiDevice)
	case AccelQSV:
		args = append(args, "-hwaccel", "qsv")
	}
	args = append(args, "-i", source, "-map", "0:v:0", "-map", "0:a:0?")
	switch accel {
	case AccelVideoToolbox:
		args = append(args, "-c:v", "h264_videotoolbox", "-allow_sw", "1", "-realtime", "1", "-b:v", bitrate)
	case AccelVAAPI:
		args = append(args, "-vf", "format=nv12,hwupload", "-c:v", "h264_vaapi")
	case AccelNVENC:
		args = append(args, "-c:v", "h264_nvenc", "-preset", "p4", "-rc", "vbr", "-b:v", bitrate)
	case AccelQSV:
		args = append(args, "-c:v", "h264_qsv")
	default:
		args = append(args, "-c:v", "libx264", "-preset", "veryfast", "-crf", "21")
	}
	args = append(args,
		"-c:a", "aac", "-b:a", "192k", "-ac", "2",
		"-muxdelay", "0",
		"-f", "hls",
		"-hls_time", "4",
		"-hls_init_time", "2",
		"-hls_list_size", "0",
		"-hls_flags", "independent_segments",
		"-hls_segment_filename", filepath.Join(dir, "seg%05d.ts"),
		filepath.Join(dir, "index.m3u8"),
	)
	return args
}

// SetHwAccel pins the accel mode (Accel* constants, or "auto" to restore
// detection). Takes precedence over LIBTECA_HWACCEL. Applies to new sessions;
// running sessions are unaffected.
func (m *Manager) SetHwAccel(accel string) error {
	switch accel {
	case AccelNone, AccelVideoToolbox, AccelVAAPI, AccelNVENC, AccelQSV:
	case "auto":
		accel = ""
	default:
		return fmt.Errorf("transcode: unknown hwaccel %q", accel)
	}
	m.hwMu.Lock()
	defer m.hwMu.Unlock()
	m.hwSet = accel
	m.hwMode = accel
	return nil
}

// HwAccel reports the resolved accel mode, detecting once on first use.
func (m *Manager) HwAccel() string { return m.accelMode() }

func (m *Manager) accelMode() string {
	m.hwMu.Lock()
	defer m.hwMu.Unlock()
	if m.hwMode == "" {
		m.hwMode = m.detectAccel()
	}
	return m.hwMode
}

func (m *Manager) detectAccel() string {
	if m.hwSet != "" {
		return m.hwSet
	}
	run := m.probeRun
	if run == nil {
		return AccelNone
	}
	hwOut, hwErr := run([]string{"-hide_banner", "-hwaccels"})
	encOut, encErr := run([]string{"-hide_banner", "-encoders"})
	if hwErr != nil || encErr != nil {
		slog.Warn("transcode: ffmpeg hwaccel probe failed, using software", "hwaccels", hwErr, "encoders", encErr)
		return AccelNone
	}
	mode := selectAccel(runtime.GOOS, renderNodeExists(),
		hwProbe{hwaccels: parseHwaccels(hwOut), encoders: parseEncoders(encOut)},
		os.Getenv(envHwAccel))
	slog.Info("transcode: hwaccel selected", "mode", mode)
	return mode
}
