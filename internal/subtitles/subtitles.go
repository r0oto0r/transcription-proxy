package subtitles

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/ben/transcription-proxy/internal/transcriber"
)

type SubtitleFormat string

const (
	FormatSRT  SubtitleFormat = "srt"
	FormatVTT  SubtitleFormat = "vtt"
	FormatNone SubtitleFormat = "none"
)

type SubtitleEmbedder struct {
	format SubtitleFormat
}

func New(format SubtitleFormat) *SubtitleEmbedder {
	return &SubtitleEmbedder{
		format: format,
	}
}

func (e *SubtitleEmbedder) EmbedSubtitles(videoData []byte, segments []transcriber.Segment) ([]byte, error) {
	if e.format == FormatNone || len(segments) == 0 {
		return videoData, nil
	}

	subtitleBytes, err := e.generateSubtitleData(segments)
	if err != nil {
		return nil, fmt.Errorf("failed to generate subtitles: %w", err)
	}

	return e.embedSubtitleDataIntoVideo(videoData, subtitleBytes)
}

func (e *SubtitleEmbedder) generateSubtitleData(segments []transcriber.Segment) ([]byte, error) {
	var buf bytes.Buffer

	switch e.format {
	case FormatSRT:
		for i, segment := range segments {
			startTime := formatSRTTime(segment.Start)
			endTime := formatSRTTime(segment.End)

			fmt.Fprintf(&buf, "%d\n", i+1)
			fmt.Fprintf(&buf, "%s --> %s\n", startTime, endTime)
			fmt.Fprintf(&buf, "%s\n\n", segment.Text)
		}
	case FormatVTT:
		fmt.Fprintln(&buf, "WEBVTT")
		fmt.Fprintln(&buf, "")

		for i, segment := range segments {
			startTime := formatVTTTime(segment.Start)
			endTime := formatVTTTime(segment.End)

			fmt.Fprintf(&buf, "%d\n", i+1)
			fmt.Fprintf(&buf, "%s --> %s\n", startTime, endTime)
			fmt.Fprintf(&buf, "%s\n\n", segment.Text)
		}
	default:
		return nil, fmt.Errorf("unsupported subtitle format: %s", e.format)
	}

	return buf.Bytes(), nil
}

func (e *SubtitleEmbedder) embedSubtitleDataIntoVideo(videoData, subtitleData []byte) ([]byte, error) {
	// Create temporary directory for processing
	tempDir, err := os.MkdirTemp("", "subtitle-embedding")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp directory: %w", err)
	}
	defer os.RemoveAll(tempDir)

	// Write video data to temp file
	videoPath := filepath.Join(tempDir, "input.mp4")
	if err := os.WriteFile(videoPath, videoData, 0644); err != nil {
		return nil, fmt.Errorf("failed to write video to temp file: %w", err)
	}

	// Write subtitle data to temp file
	var subtitleExt string
	switch e.format {
	case FormatSRT:
		subtitleExt = "srt"
	case FormatVTT:
		subtitleExt = "vtt"
	default:
		return nil, fmt.Errorf("unsupported subtitle format: %s", e.format)
	}

	subtitlePath := filepath.Join(tempDir, fmt.Sprintf("subtitles.%s", subtitleExt))
	if err := os.WriteFile(subtitlePath, subtitleData, 0644); err != nil {
		return nil, fmt.Errorf("failed to write subtitles to temp file: %w", err)
	}

	// Output path for processed video
	outputPath := filepath.Join(tempDir, "output.mp4")

	// Use FFmpeg to embed subtitles
	args := []string{
		"-i", videoPath,
		"-i", subtitlePath,
		"-c:v", "copy", // Copy video codec
		"-c:a", "copy", // Copy audio codec
		"-c:s", "mov_text", // Use mov_text codec for subtitles
		"-metadata:s:s:0", "language=eng", // Set subtitle language to English
		"-y", // Overwrite output file if it exists
		outputPath,
	}

	cmd := exec.Command("ffmpeg", args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("ffmpeg failed: %w, stderr: %s", err, stderr.String())
	}

	// Read the processed video
	processedVideo, err := os.ReadFile(outputPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read processed video: %w", err)
	}

	return processedVideo, nil
}

func formatSRTTime(seconds float64) string {
	duration := time.Duration(seconds * float64(time.Second))
	h := int(duration.Hours())
	m := int(duration.Minutes()) % 60
	s := int(duration.Seconds()) % 60
	ms := int(duration.Milliseconds()) % 1000

	return fmt.Sprintf("%02d:%02d:%02d,%03d", h, m, s, ms)
}

func formatVTTTime(seconds float64) string {
	duration := time.Duration(seconds * float64(time.Second))
	h := int(duration.Hours())
	m := int(duration.Minutes()) % 60
	s := int(duration.Seconds()) % 60
	ms := int(duration.Milliseconds()) % 1000

	return fmt.Sprintf("%02d:%02d:%02d.%03d", h, m, s, ms)
}
