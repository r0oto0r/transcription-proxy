package subtitles

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
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
	// Setup FFmpeg command with input pipes
	args := []string{
		"-loglevel", "warning", // Reduce log noise
		"-i", "pipe:0", // Read video from stdin without specifying format
		"-f", subtitleFormatToFFmpegFormat(e.format), // Specify subtitle format
		"-i", "pipe:3", // Read subtitles from file descriptor 3
		"-c:v", "copy", // Copy video codec
		"-c:a", "copy", // Copy audio codec
		"-c:s", "mov_text", // Use mov_text codec for subtitles
		"-metadata:s:s:0", "language=eng", // Set subtitle language to English
		"-y",        // Overwrite output file if it exists
		"-f", "flv", // Specify FLV output format (better for streaming)
		"pipe:1", // Output to stdout
	}

	cmd := exec.Command("ffmpeg", args...)

	// Setup the stdin pipe for video data
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("failed to create stdin pipe: %w", err)
	}

	// Setup stdout pipe to capture the processed video
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("failed to create stdout pipe: %w", err)
	}

	// Setup stderr pipe to capture any error messages
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("failed to create stderr pipe: %w", err)
	}

	// Create a pipe for subtitle data
	subtitleRead, subtitleWrite, err := os.Pipe()
	if err != nil {
		return nil, fmt.Errorf("failed to create subtitle pipe: %w", err)
	}
	defer subtitleRead.Close()
	defer subtitleWrite.Close()

	// Assign the read end of the pipe to the extra file descriptor
	cmd.ExtraFiles = []*os.File{subtitleRead}

	// Start the command
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("failed to start ffmpeg: %w", err)
	}

	// Write video data to stdin in a goroutine
	go func() {
		defer stdin.Close()
		if _, err := stdin.Write(videoData); err != nil {
			// Don't print to stderr, just silently handle the error
			_ = err
		}
	}()

	// Write subtitle data to the subtitle pipe in a goroutine
	go func() {
		defer subtitleWrite.Close()
		if _, err := subtitleWrite.Write(subtitleData); err != nil {
			// Don't print to stderr, just silently handle the error
			_ = err
		}
	}()

	// Read processed video from stdout
	var output bytes.Buffer
	if _, err := io.Copy(&output, stdout); err != nil {
		return nil, fmt.Errorf("failed to read processed video: %w", err)
	}

	// Read stderr for error messages
	var stderrOutput bytes.Buffer
	if _, err := io.Copy(&stderrOutput, stderr); err != nil {
		return nil, fmt.Errorf("failed to read stderr: %w", err)
	}

	// Wait for the command to complete
	err = cmd.Wait()
	if err != nil {
		return nil, fmt.Errorf("ffmpeg failed: %w, stderr: %s", err, stderrOutput.String())
	}

	return output.Bytes(), nil
}

// Helper function to convert subtitle format to FFmpeg format
func subtitleFormatToFFmpegFormat(format SubtitleFormat) string {
	switch format {
	case FormatSRT:
		return "srt"
	case FormatVTT:
		return "webvtt"
	default:
		return "srt"
	}
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
