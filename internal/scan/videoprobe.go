package scan

import (
	"encoding/json"
	"os"
	"os/exec"

	"github.com/libteca/libteca/internal/procfd"
)

type vpOut struct {
	Streams []struct {
		CodecName string `json:"codec_name"`
		CodecType string `json:"codec_type"`
		Width     int    `json:"width"`
		Height    int    `json:"height"`
	} `json:"streams"`
}

func probeVideoFile(f *os.File) (string, int, int) {
	inArgs, err := procfd.Args("ffprobe")
	if err != nil {
		return "", 0, 0
	}
	args := append([]string{"-v", "quiet", "-print_format", "json", "-show_streams", "-select_streams", "v:0"}, inArgs...)
	cmd := exec.Command("ffprobe", args...)
	cmd.ExtraFiles = []*os.File{f}
	out, err := cmd.Output()
	if err != nil {
		return "", 0, 0
	}
	var p vpOut
	if json.Unmarshal(out, &p) != nil {
		return "", 0, 0
	}
	for _, s := range p.Streams {
		return s.CodecName, s.Width, s.Height
	}
	return "", 0, 0
}
