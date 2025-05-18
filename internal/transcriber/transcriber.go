package transcriber

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/ben/transcription-proxy/internal/config"
)

type Transcriber struct {
	config    *config.Config
	modelPath string
}

type Segment struct {
	ID        int
	Start     float64
	End       float64
	Text      string
	Timestamp string
}

func New(cfg *config.Config) *Transcriber {
	modelPath := filepath.Join(cfg.WhisperModelPath, cfg.WhisperModelSize)
	return &Transcriber{
		config:    cfg,
		modelPath: modelPath,
	}
}

func (t *Transcriber) TranscribeAudio(audioBytes []byte, lang string) ([]Segment, error) {
	tempDir, err := os.MkdirTemp("", "transcription")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp directory: %w", err)
	}
	defer os.RemoveAll(tempDir)

	// Save input stream to a temporary file
	inputPath := filepath.Join(tempDir, "input.mp4")
	if err := os.WriteFile(inputPath, audioBytes, 0644); err != nil {
		return nil, fmt.Errorf("failed to write input to file: %w", err)
	}

	// Extract audio using FFmpeg
	audioPath := filepath.Join(tempDir, "audio.wav")
	ffmpegArgs := []string{
		"-i", inputPath,
		"-vn",                  // Skip video
		"-acodec", "pcm_s16le", // Use PCM 16-bit audio codec
		"-ar", "16000", // Set sample rate to 16kHz
		"-ac", "1", // Convert to mono
		"-y", // Overwrite output file
		audioPath,
	}

	cmd := exec.Command("ffmpeg", ffmpegArgs...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("failed to extract audio: %w, output: %s", err, string(output))
	}

	// Use faster-whisper to transcribe the audio with optimized settings for shared VRAM
	args := []string{
		"--model", t.modelPath,
		"--device", "cuda",
		"--language", lang,
		"--output_format", "txt",
		"--vad_filter", "true", // Voice activity detection to filter non-speech
		"--word_timestamps", "true", // Generate timestamps at the word level
		"--compute_type", t.config.ComputePrecision, // float16 for optimal performance vs memory usage
		"--beam_size", fmt.Sprintf("%d", t.config.BeamSize), // Beam size for better accuracy
		"--best_of", fmt.Sprintf("%d", t.config.BeamSize), // Return best result from beam search
		"--vad_parameters", "{\"threshold\": 0.5}", // Adjust VAD for better segment detection
		"--threads", fmt.Sprintf("%d", t.config.GPUThreads), // Control CPU threads for processing
	}

	// Set GPU-specific optimizations
	if t.config.CUDAEnabled {
		// Get device type (CUDA)
		args[3] = "cuda"

		// Add GPU-specific optimizations
		args = append(args, "--device_index", "0") // Use first GPU
		args = append(args, "--cpu_threads", "4")  // CPU threads for non-GPU work

		// Set batch size for parallel processing
		args = append(args, "--batch_size", fmt.Sprintf("%d", t.config.BatchSize))

		// Set maximum VRAM usage (in MB) - reduced to allow for translation model
		if t.config.MaxVRAMUsageMB > 0 {
			args = append(args, "--gpu_vram_limit", fmt.Sprintf("%d", t.config.MaxVRAMUsageMB))
		}
	} else {
		args[3] = "cpu"
		// Set CPU-specific optimizations
		args = append(args, "--cpu_threads", "8") // More CPU threads when not using GPU
	}

	// Add the audio file path as the final argument
	args = append(args, audioPath)

	cmd = exec.Command("faster-whisper", args...)
	outputBytes, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("transcription failed: %w, output: %s", err, string(outputBytes))
	}

	// Save to output directory with timestamp
	if t.config.OutputDir != "" {
		timestamp := time.Now().Format("20060102-150405")
		outputFilename := filepath.Join(t.config.OutputDir, fmt.Sprintf("transcript-%s.txt", timestamp))

		if err := os.MkdirAll(t.config.OutputDir, 0755); err != nil {
			return nil, fmt.Errorf("failed to create output directory: %w", err)
		}

		transcriptPath := audioPath + ".txt"
		if _, err := os.Stat(transcriptPath); err == nil {
			// Read transcript file
			transcriptBytes, err := os.ReadFile(transcriptPath)
			if err != nil {
				return nil, fmt.Errorf("failed to read transcript file: %w", err)
			}

			// Save transcript
			if err := os.WriteFile(outputFilename, transcriptBytes, 0644); err != nil {
				return nil, fmt.Errorf("failed to save transcript: %w", err)
			}

			// Parse segments from transcript
			return parseTranscript(string(transcriptBytes)), nil
		} else {
			// If no transcript file was generated, save the command output
			if err := os.WriteFile(outputFilename, outputBytes, 0644); err != nil {
				return nil, fmt.Errorf("failed to save transcript: %w", err)
			}

			// Try to parse segments from command output
			return parseTranscript(string(outputBytes)), nil
		}
	}

	// If no output directory is specified or transcript file wasn't found, parse from command output
	return parseTranscript(string(outputBytes)), nil
}

func parseTranscript(transcript string) []Segment {
	lines := strings.Split(transcript, "\n")
	segments := make([]Segment, 0, len(lines))

	for i, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}

		parts := strings.SplitN(line, " ", 3)
		if len(parts) < 3 {
			continue
		}

		timeRange := strings.Trim(parts[0], "[]")
		timeParts := strings.Split(timeRange, "-->")
		if len(timeParts) != 2 {
			continue
		}

		startTime, _ := parseTimestamp(strings.TrimSpace(timeParts[0]))
		endTime, _ := parseTimestamp(strings.TrimSpace(timeParts[1]))

		segment := Segment{
			ID:        i,
			Start:     startTime,
			End:       endTime,
			Text:      strings.TrimSpace(parts[2]),
			Timestamp: timeRange,
		}

		segments = append(segments, segment)
	}

	return segments
}

func parseTimestamp(timestamp string) (float64, error) {
	parts := strings.Split(timestamp, ":")
	if len(parts) != 3 {
		return 0, fmt.Errorf("invalid timestamp format: %s", timestamp)
	}

	hours := 0.0
	minutes := 0.0
	seconds := 0.0

	fmt.Sscanf(parts[0], "%f", &hours)
	fmt.Sscanf(parts[1], "%f", &minutes)
	fmt.Sscanf(parts[2], "%f", &seconds)

	return hours*3600 + minutes*60 + seconds, nil
}

func (t *Transcriber) TranslateSegments(segments []Segment, targetLang string) ([]Segment, error) {
	// In a real implementation, you would call a translation service
	// For now, we'll just return the original segments
	return segments, nil
}
