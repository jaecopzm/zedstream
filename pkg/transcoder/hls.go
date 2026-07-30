package transcoder

import (
	"fmt"
	"os/exec"
	"path/filepath"
)

// ProcessHLS takes an input audio file and generates an HLS playlist and segments
// inside the outputDir. It creates bitrates of 128k, 192k, and 320k.
func ProcessHLS(inputFile string, outputDir string) error {

	// ffmpeg command to convert audio to HLS segments with multiple bitrates
	// This command generates an HLS playlist using aac encoding.
	cmd := exec.Command("ffmpeg",
		"-hide_banner", "-y",
		"-i", inputFile,
		"-map", "0:a", "-map", "0:a", "-map", "0:a",
		"-c:a", "aac",
		"-b:a:0", "128k",
		"-b:a:1", "192k",
		"-b:a:2", "320k",
		"-f", "hls",
		"-hls_time", "6", // 6 second segments
		"-hls_playlist_type", "vod",
		"-hls_segment_filename", filepath.Join(outputDir, "stream_%v_%03d.ts"),
		"-master_pl_name", "master.m3u8",
		"-var_stream_map", "a:0,agroup:audio,name:128k a:1,agroup:audio,name:192k a:2,agroup:audio,name:320k",
		filepath.Join(outputDir, "stream_%v.m3u8"),
	)

	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("ffmpeg failed: %w, output: %s", err, string(out))
	}

	return nil
}
