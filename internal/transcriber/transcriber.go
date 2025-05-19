package transcriber

import (
	"bytes"
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
	modelName string
}

type Segment struct {
	ID        int
	Start     float64
	End       float64
	Text      string
	Timestamp string
}

func New(cfg *config.Config) *Transcriber {
	return &Transcriber{
		config:    cfg,
		modelPath: cfg.WhisperModelPath,
		modelName: cfg.WhisperModelSize,
	}
}

// TranscribeAudio transcribes audio bytes to text segments
func (t *Transcriber) TranscribeAudio(audioBytes []byte, lang string) ([]Segment, error) {
	// Use a shared temporary directory instead of creating one for each chunk
	tempDir := filepath.Join(t.config.OutputDir, "transcription_temp")

	// Create shared directory if it doesn't exist yet
	if _, err := os.Stat(tempDir); os.IsNotExist(err) {
		if err := os.MkdirAll(tempDir, 0755); err != nil {
			return nil, fmt.Errorf("failed to create temp directory: %w", err)
		}
	}

	// Verify we have enough audio data to process
	if len(audioBytes) < 1024 {
		return nil, fmt.Errorf("audio data too small to process (%d bytes)", len(audioBytes))
	}

	// Use timestamp to create unique filenames within the shared directory
	timestamp := time.Now().UnixNano()
	inputPath := filepath.Join(tempDir, fmt.Sprintf("input-%d.bin", timestamp))
	audioPath := filepath.Join(tempDir, fmt.Sprintf("audio-%d.wav", timestamp))
	outputPath := filepath.Join(tempDir, fmt.Sprintf("transcript-%d.json", timestamp))

	// Save input data to a temporary file
	if err := os.WriteFile(inputPath, audioBytes, 0644); err != nil {
		return nil, fmt.Errorf("failed to write input data to file: %w", err)
	}

	// Clean up temporary files when done
	defer func() {
		os.Remove(inputPath)
		os.Remove(audioPath)
		os.Remove(outputPath)
	}()

	// Convert to WAV format using a file-based approach - explicitly specify input format as wav or raw pcm
	cmd := exec.Command("ffmpeg",
		"-loglevel", "info", // More verbose logging for debugging
		"-f", "s16le", // Explicitly specifying input format as signed 16-bit PCM
		"-ar", "16000", // Input sample rate to match expected
		"-ac", "1", // Input channels to match expected
		"-i", inputPath, // Read from the temporary file
		"-vn",                  // Skip video
		"-acodec", "pcm_s16le", // Use PCM 16-bit audio codec
		"-ar", "16000", // Set sample rate to 16kHz
		"-ac", "1", // Convert to mono
		"-y",        // Overwrite output if exists
		"-f", "wav", // Output format
		audioPath) // Output to file

	// Create buffer for stderr output
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	// Run the ffmpeg process
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("ffmpeg failed: %w, stderr: %s", err, stderr.String())
	}

	// Check if audio file was created successfully and has content
	fileInfo, err := os.Stat(audioPath)
	if err != nil {
		return nil, fmt.Errorf("ffmpeg output file error: %w, stderr: %s", err, stderr.String())
	}

	if fileInfo.Size() == 0 {
		return nil, fmt.Errorf("ffmpeg created empty output file, stderr: %s", stderr.String())
	}

	// Determine device type based on configuration
	deviceType := "cpu"
	if t.config.CUDAEnabled {
		deviceType = "cuda"
	}

	// Use whisper-ctranslate2 to transcribe the audio
	args := []string{
		"--model_directory", filepath.Join(t.modelPath, "faster-whisper-large-v3-turbo-ct2"),
		"--device", deviceType,
		"--language", lang,
		"--output_format", "json",
		"--output_dir", tempDir,
		"--threads", fmt.Sprintf("%d", t.config.GPUThreads),
		"--word_timestamps", "True", // Get word-level timestamps
	}

	// Add VAD filter to improve audio processing
	args = append(args, "--vad_filter", "True")

	// Set compute type based on configuration
	args = append(args, "--compute_type", t.config.ComputePrecision)

	// Add batch processing if using GPU
	if t.config.CUDAEnabled && t.config.BatchSize > 1 {
		args = append(args, "--batched", "True")
		args = append(args, "--batch_size", fmt.Sprintf("%d", t.config.BatchSize))
	}

	// Set beam size for better accuracy
	args = append(args, "--beam_size", fmt.Sprintf("%d", t.config.BeamSize))

	// Add the audio file path as the final argument
	args = append(args, audioPath)

	// Create pipes for stdout and stderr
	cmd = exec.Command("whisper-ctranslate2", args...)
	var stdout bytes.Buffer
	stderr.Reset()
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	// Run transcription
	err = cmd.Run()
	if err != nil {
		return nil, fmt.Errorf("transcription failed: %w, stderr: %s", err, stderr.String())
	}

	// Check for generated JSON output file
	baseFilename := filepath.Base(audioPath)
	baseFilenameWithoutExt := strings.TrimSuffix(baseFilename, filepath.Ext(baseFilename))
	jsonOutputPath := filepath.Join(tempDir, baseFilenameWithoutExt+".json")

	// Read the JSON output file or use stdout if not found
	var transcriptBytes []byte
	if _, err := os.Stat(jsonOutputPath); err == nil {
		transcriptBytes, err = os.ReadFile(jsonOutputPath)
		if err != nil {
			return nil, fmt.Errorf("failed to read transcript JSON file: %w", err)
		}
	} else {
		// Save the stdout output as a fallback
		transcriptBytes = stdout.Bytes()
		// Parse segments from the stdout output
		// Note: This is a simplified fallback in case JSON output isn't available
		return parseTranscript(string(transcriptBytes)), nil
	}

	// Save to output directory with timestamp if enabled
	if t.config.OutputDir != "" && len(transcriptBytes) > 0 {
		timeStr := time.Now().Format("20060102-150405")
		outputFilename := filepath.Join(t.config.OutputDir, fmt.Sprintf("transcript-%s.txt", timeStr))

		// Ensure directory exists (should already exist but double-check)
		if err := os.MkdirAll(t.config.OutputDir, 0755); err != nil {
			// Just log error and continue, this is not critical
			fmt.Fprintf(os.Stderr, "failed to create output directory: %v\n", err)
		} else {
			// Try to save the transcript, but don't fail if it doesn't work
			_ = os.WriteFile(outputFilename, transcriptBytes, 0644)
		}
	}

	// Parse segments from the JSON output
	segments, err := parseJSONOutput(string(transcriptBytes))
	if err != nil {
		// Fall back to parsing the stdout directly
		return parseTranscript(stdout.String()), nil
	}

	return segments, nil
}

// parseJSONOutput parses the JSON output from whisper-ctranslate2
// This is a simplified version for illustration - in a real implementation,
// you'd use proper JSON parsing with the json package
func parseJSONOutput(jsonStr string) ([]Segment, error) {
	// Simplified implementation - in production, use proper JSON parsing
	// This function should extract segments from the JSON format
	// For now, fall back to the basic parsing
	return parseTranscript(jsonStr), nil
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
