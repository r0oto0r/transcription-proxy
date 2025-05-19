// Package proxy provides an RTMP server that uses FFmpeg as a listener.
// This implementation is designed to handle a single stream at a time,
// transcribe the audio using Whisper, optionally translate the transcription,
// embed subtitles into the video, and forward the processed stream to target URLs.
package proxy

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/ben/transcription-proxy/internal/config"
	"github.com/ben/transcription-proxy/internal/streaming"
	"github.com/ben/transcription-proxy/internal/subtitles"
	"github.com/ben/transcription-proxy/internal/transcriber"
	"github.com/ben/transcription-proxy/internal/translator"
	"github.com/sirupsen/logrus"
)

// Proxy represents an RTMP server that handles incoming streams
type Proxy struct {
	Config      *config.Config `json:"config"`
	transcriber *transcriber.Transcriber
	translator  *translator.Translator
	embedder    *subtitles.SubtitleEmbedder
	logger      *logrus.Logger
	ffmpegCmd   *exec.Cmd
	stopChan    chan struct{}
}

// New creates a new RTMP server
func New(cfg *config.Config) *Proxy {
	logger := logrus.New()

	level, err := logrus.ParseLevel(cfg.LogLevel)
	if err != nil {
		level = logrus.InfoLevel
	}
	logger.SetLevel(level)

	server := &Proxy{
		Config:      cfg,
		transcriber: transcriber.New(cfg),
		translator:  translator.New(cfg),
		embedder:    subtitles.New(subtitles.FormatSRT),
		logger:      logger,
		stopChan:    make(chan struct{}),
	}

	return server
}

// Start starts the RTMP server using FFmpeg as the listener
func (p *Proxy) Start() error {
	p.logger.WithField("port", p.Config.RTMPPort).Info("Starting FFmpeg-based RTMP server")

	// Create temp directory for FFmpeg temporary files if needed
	tempDir := fmt.Sprintf("%s/ffmpeg_temp", p.Config.OutputDir)
	if err := os.MkdirAll(tempDir, 0755); err != nil {
		return fmt.Errorf("failed to create temp directory: %w", err)
	}

	// Create pipes for audio and video
	audioPipeReader, audioPipeWriter := io.Pipe()
	videoPipeReader, videoPipeWriter := io.Pipe()

	// Start FFmpeg as an RTMP server
	args := []string{
		"-y", // Force overwrite output files
		"-listen", "1",
		"-f", "flv",
		"-i", fmt.Sprintf("rtmp://0.0.0.0:%s/live/stream", p.Config.RTMPPort),

		// Audio output for transcription
		"-map", "0:a",
		"-c:a", "pcm_s16le",
		"-ar", "16000",
		"-ac", "1",
		"-f", "wav",
		"pipe:1", // Output to stdout for audio

		// Video output (preserved for later subtitle embedding)
		"-map", "0:v",
		"-c:v", "copy",
		"-f", "flv", // Using FLV format for video output
		"pipe:2", // Output to stderr for video
	}

	p.logger.WithField("args", args).Debug("Starting FFmpeg command")
	cmd := exec.Command("ffmpeg", args...)

	// Set up pipe for FFmpeg's stdout (audio data)
	cmd.Stdout = audioPipeWriter

	// Set up pipe for FFmpeg's stderr (video data and logs)
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("failed to create FFmpeg stderr pipe: %w", err)
	}

	// Only sending stderr to the video pipe writer, not to os.Stderr to avoid printing error logs
	// This redirects all FFmpeg error logs away from the terminal
	videoWriter := videoPipeWriter

	// Start a goroutine to read from stderr pipe and write to the video pipe only
	go func() {
		defer videoPipeWriter.Close()
		if _, err := io.Copy(videoWriter, stderrPipe); err != nil {
			p.logger.WithError(err).Error("Failed to copy from stderr pipe")
		}
	}()

	// Start FFmpeg
	if err := cmd.Start(); err != nil {
		audioPipeWriter.Close()
		videoPipeWriter.Close()
		audioPipeReader.Close()
		videoPipeReader.Close()
		return fmt.Errorf("failed to start FFmpeg: %w", err)
	}
	p.ffmpegCmd = cmd

	p.logger.Info("FFmpeg RTMP server started successfully")

	// Start processing the pipes in a goroutine
	go p.processFFmpegOutput(audioPipeReader, videoPipeReader)

	return nil
}

// Stop stops the RTMP server
func (p *Proxy) Stop() error {
	if p.ffmpegCmd != nil && p.ffmpegCmd.Process != nil {
		close(p.stopChan)

		p.logger.Info("Stopping FFmpeg RTMP server")
		if err := p.ffmpegCmd.Process.Signal(os.Interrupt); err != nil {
			p.logger.WithError(err).Warning("Failed to send interrupt to FFmpeg, forcing kill")
			if err := p.ffmpegCmd.Process.Kill(); err != nil {
				return fmt.Errorf("failed to kill FFmpeg process: %w", err)
			}
		}

		// Wait for process to exit
		p.ffmpegCmd.Wait()
		p.logger.Info("FFmpeg RTMP server stopped")
	}
	return nil
}

// processFFmpegOutput handles the audio and video data from FFmpeg pipes
func (p *Proxy) processFFmpegOutput(audioReader, videoReader io.ReadCloser) {
	defer audioReader.Close()
	defer videoReader.Close()

	logger := p.logger.WithFields(logrus.Fields{
		"processor": "ffmpeg-output",
	})

	logger.Info("Waiting for incoming RTMP stream")

	// Create a stream connection object
	streamKey := fmt.Sprintf("stream-%d", time.Now().UnixNano())
	streamConn := &rtmpConnection{
		streamName:   streamKey,
		sourceURL:    fmt.Sprintf("rtmp://localhost:%s/live/stream", p.Config.RTMPPort),
		targetURL:    p.Config.DefaultTargetURL,
		sourceLang:   p.Config.DefaultSourceLang,
		targetLang:   p.Config.DefaultTargetLang,
		subtitleType: subtitles.FormatSRT,
	}

	// Parse target URLs once at the beginning
	streamTargets, err := parseTargetURLs(p.Config.DefaultTargetURL)
	if err != nil {
		logger.WithError(err).Error("Invalid target URL")
		return
	}

	// Create the streaming client
	streamer := streaming.New(streamTargets)

	// Create buffers for audio and video
	const chunkDuration = 10 * time.Second // Process in 10-second chunks
	const audioSampleRate = 16000          // 16kHz sample rate
	const bytesPerSample = 2               // 16-bit audio = 2 bytes per sample
	const channels = 1                     // Mono audio

	// Calculate buffer size for audio (bytes for a chunk duration at given sample rate)
	audioChunkSize := int(chunkDuration.Seconds() * float64(audioSampleRate) * float64(bytesPerSample) * float64(channels))

	// Buffer for video (will be variable size but need to store it)
	var videoBuffer bytes.Buffer

	// Buffer for collecting audio chunks
	audioChunk := make([]byte, audioChunkSize)

	// Track audio bytes read
	var totalAudioBytesRead int

	// Channel to signal when audio chunk is ready for processing
	audioChunkReady := make(chan struct{})

	// Create a buffer pool for processed video chunks
	processedChunks := make(chan []byte, 3) // Buffer up to 3 processed chunks

	// WaitGroup to wait for all goroutines to finish when shutting down
	var wg sync.WaitGroup

	// Start goroutine to continuously collect video data
	wg.Add(1)
	go func() {
		defer wg.Done()

		buffer := make([]byte, 64*1024) // 64KB read buffer
		for {
			select {
			case <-p.stopChan:
				return
			default:
				n, err := videoReader.Read(buffer)
				if err != nil {
					if err != io.EOF {
						logger.WithError(err).Error("Error reading video data")
					}
					return
				}

				if n > 0 {
					videoBuffer.Write(buffer[:n])
				}
			}
		}
	}()

	// Start goroutine to read audio data in chunks
	wg.Add(1)
	go func() {
		defer wg.Done()

		for {
			select {
			case <-p.stopChan:
				return
			default:
				n, err := audioReader.Read(audioChunk[totalAudioBytesRead:])
				if err != nil {
					if err != io.EOF {
						logger.WithError(err).Error("Error reading audio data")
					}
					return
				}

				if n > 0 {
					totalAudioBytesRead += n

					// If we've read a full chunk, signal it's ready for processing
					if totalAudioBytesRead >= audioChunkSize {
						select {
						case audioChunkReady <- struct{}{}:
							// Signal sent
						case <-p.stopChan:
							return
						}

						// Reset counter for next chunk
						totalAudioBytesRead = 0
					}
				}
			}
		}
	}()

	// Start goroutine to process audio chunks and video data
	wg.Add(1)
	go func() {
		defer wg.Done()

		var videoChunk []byte

		for {
			select {
			case <-p.stopChan:
				return

			case <-audioChunkReady:
				// Copy the current video buffer for this audio chunk
				videoChunk = make([]byte, videoBuffer.Len())
				copy(videoChunk, videoBuffer.Bytes())

				// Reset video buffer for next chunk
				videoBuffer.Reset()

				// Process this chunk in a separate goroutine
				wg.Add(1)
				go func(audio []byte, video []byte) {
					defer wg.Done()

					chunkLogger := logger.WithField("chunk_size_bytes", len(audio))
					chunkLogger.Info("Processing audio/video chunk")

					// If the audio or video chunk is too small, skip processing
					if len(audio) < 1000 || len(video) < 1000 {
						chunkLogger.Warn("Chunk too small, skipping processing")
						// Still forward the video for continuity
						select {
						case processedChunks <- video:
							// Chunk queued for streaming
						case <-p.stopChan:
							return
						}
						return
					}

					// Transcribe the audio chunk with retries
					var segments []transcriber.Segment
					var err error
					maxRetries := 3

					for i := 0; i < maxRetries; i++ {
						segments, err = p.transcriber.TranscribeAudio(audio, streamConn.sourceLang)
						if err == nil {
							break
						}

						chunkLogger.WithError(err).Warnf("Transcription attempt %d failed, retrying...", i+1)
						time.Sleep(100 * time.Millisecond) // Small delay between retries
					}

					if err != nil {
						chunkLogger.WithError(err).Error("Chunk transcription failed after retries")
						// Forward original video chunk if transcription fails
						select {
						case processedChunks <- video:
							// Chunk queued for streaming
						case <-p.stopChan:
							return
						}
						return
					}

					// Translate if needed
					if streamConn.targetLang != "" && streamConn.targetLang != streamConn.sourceLang {
						translatedSegments, err := p.translator.TranslateSegments(segments, streamConn.sourceLang, streamConn.targetLang)
						if err != nil {
							chunkLogger.WithError(err).Error("Translation failed, using original transcription")
						} else {
							segments = translatedSegments
						}
					}

					// Embed subtitles into video chunk with retries
					var processedVideo []byte
					for i := 0; i < maxRetries; i++ {
						processedVideo, err = p.embedder.EmbedSubtitles(video, segments)
						if err == nil {
							break
						}

						chunkLogger.WithError(err).Warnf("Subtitle embedding attempt %d failed, retrying...", i+1)
						time.Sleep(100 * time.Millisecond) // Small delay between retries
					}

					if err != nil {
						chunkLogger.WithError(err).Error("Failed to embed subtitles after retries, using original video")
						select {
						case processedChunks <- video:
							// Chunk queued for streaming
						case <-p.stopChan:
							return
						}
						return
					}

					// Queue the processed chunk for streaming
					select {
					case processedChunks <- processedVideo:
						chunkLogger.Info("Chunk processed and queued for streaming")
					case <-p.stopChan:
						return
					}
				}(append([]byte{}, audioChunk[:audioChunkSize]...), videoChunk)
			}
		}
	}()

	// Start goroutine to stream processed chunks
	wg.Add(1)
	go func() {
		defer wg.Done()

		for {
			select {
			case <-p.stopChan:
				return

			case chunk := <-processedChunks:
				chunkLogger := logger.WithField("chunk_size", len(chunk))
				chunkLogger.Info("Streaming processed chunk")

				// Try multiple times to stream the chunk
				var err error
				maxRetries := 3

				for i := 0; i < maxRetries; i++ {
					err = streamer.Stream(chunk)
					if err == nil {
						break
					}

					chunkLogger.WithError(err).Warnf("Streaming attempt %d failed, retrying...", i+1)
					time.Sleep(100 * time.Millisecond) // Small delay between retries
				}

				if err != nil {
					chunkLogger.WithError(err).Error("Error streaming chunk after retries")
				}
			}
		}
	}()

	// Wait for all goroutines to complete when stop is called
	<-p.stopChan
	logger.Info("Stopping all stream processing goroutines")
	wg.Wait()
	logger.Info("Stream processing stopped")
}

// rtmpConnection represents an active RTMP connection
type rtmpConnection struct {
	streamName   string
	sourceURL    string
	targetURL    string
	sourceLang   string
	targetLang   string
	subtitleType subtitles.SubtitleFormat
}

// parseTargetURLs parses a comma-separated list of target URLs
func parseTargetURLs(targetURLs string) ([]*streaming.StreamTarget, error) {
	urls := bytes.Split([]byte(targetURLs), []byte(","))
	targets := make([]*streaming.StreamTarget, 0, len(urls))

	for _, urlBytes := range urls {
		urlStr := string(bytes.TrimSpace(urlBytes))
		if urlStr == "" {
			continue
		}

		target, err := streaming.ParseStreamURL(urlStr)
		if err != nil {
			return nil, fmt.Errorf("invalid target URL %q: %w", urlStr, err)
		}

		targets = append(targets, target)
	}

	if len(targets) == 0 {
		return nil, fmt.Errorf("no valid target URLs provided")
	}

	return targets, nil
}
