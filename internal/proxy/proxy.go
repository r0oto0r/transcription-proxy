package proxy

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"github.com/ben/transcription-proxy/internal/config"
	"github.com/ben/transcription-proxy/internal/streaming"
	"github.com/ben/transcription-proxy/internal/subtitles"
	"github.com/ben/transcription-proxy/internal/transcriber"
	"github.com/ben/transcription-proxy/internal/translator"
	"github.com/gorilla/mux"
	"github.com/sirupsen/logrus"
)

type Proxy struct {
	config      *config.Config
	transcriber *transcriber.Transcriber
	translator  *translator.Translator
	embedder    *subtitles.SubtitleEmbedder
	logger      *logrus.Logger
}

func New(cfg *config.Config) *Proxy {
	logger := logrus.New()

	level, err := logrus.ParseLevel(cfg.LogLevel)
	if err != nil {
		level = logrus.InfoLevel
	}
	logger.SetLevel(level)

	return &Proxy{
		config:      cfg,
		transcriber: transcriber.New(cfg),
		translator:  translator.New(cfg),
		embedder:    subtitles.New(subtitles.FormatSRT),
		logger:      logger,
	}
}

func (p *Proxy) Start() error {
	router := mux.NewRouter()

	router.HandleFunc("/health", p.handleHealth).Methods("GET")
	router.HandleFunc("/stream", p.handleStream).Methods("POST")

	p.logger.WithField("address", p.config.ListenAddress).Info("Starting transcription proxy server")
	return http.ListenAndServe(p.config.ListenAddress, router)
}

func (p *Proxy) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	fmt.Fprintln(w, "Transcription proxy server is running")
}

func (p *Proxy) handleStream(w http.ResponseWriter, r *http.Request) {
	targetURL := r.URL.Query().Get("target")
	if targetURL == "" {
		http.Error(w, "Missing target URL", http.StatusBadRequest)
		return
	}

	streamTargets, err := parseTargetURLs(targetURL)
	if err != nil {
		http.Error(w, fmt.Sprintf("Invalid target URL: %v", err), http.StatusBadRequest)
		return
	}

	// Source language for transcription
	sourceLanguage := r.URL.Query().Get("src_lang")
	if sourceLanguage == "" {
		sourceLanguage = "en"
	}

	// Target language for translation - from the 'lang' parameter
	targetLanguage := r.URL.Query().Get("lang")

	// Subtitle format selection
	subtitleFormat := r.URL.Query().Get("subtitles")
	var subtitleType subtitles.SubtitleFormat
	switch subtitleFormat {
	case "srt":
		subtitleType = subtitles.FormatSRT
	case "vtt":
		subtitleType = subtitles.FormatVTT
	case "none":
		subtitleType = subtitles.FormatNone
	default:
		subtitleType = subtitles.FormatSRT
	}

	p.embedder = subtitles.New(subtitleType)

	requestID := fmt.Sprintf("%d", time.Now().UnixNano())
	logger := p.logger.WithFields(logrus.Fields{
		"request_id":    requestID,
		"targets":       targetURL,
		"source_lang":   sourceLanguage,
		"target_lang":   targetLanguage,
		"subtitle_type": string(subtitleType),
	})

	logger.Info("Received streaming request")

	body, err := io.ReadAll(r.Body)
	if err != nil {
		logger.WithError(err).Error("Failed to read request body")
		http.Error(w, "Failed to read request body", http.StatusInternalServerError)
		return
	}
	defer r.Body.Close()

	// Process the stream in a separate goroutine
	go func() {
		if err := p.processStream(body, streamTargets, sourceLanguage, targetLanguage, logger); err != nil {
			logger.WithError(err).Error("Stream processing failed")
		} else {
			logger.Info("Stream processing completed successfully")
		}
	}()

	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, "Stream processing started with request ID: %s", requestID)
}

func (p *Proxy) processStream(data []byte, targets []*streaming.StreamTarget, srcLang, targetLang string, logger *logrus.Entry) error {
	// Create a temporary directory for processing
	tempDir, err := os.MkdirTemp("", "proxy-processing")
	if err != nil {
		return fmt.Errorf("failed to create temp directory: %w", err)
	}
	defer os.RemoveAll(tempDir)

	// Write the input data to a file
	inputPath := filepath.Join(tempDir, "input.mp4")
	if err := os.WriteFile(inputPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write input data: %w", err)
	}

	// Create channels for the processing pipeline
	audioCh := make(chan []byte, 1)
	videoCh := make(chan []byte, 1)
	resultCh := make(chan []byte, 1)
	errCh := make(chan error, 3) // For errors from goroutines

	// Split audio and video streams
	go func() {
		defer close(audioCh)
		defer close(videoCh)

		if err := p.splitAudioVideo(inputPath, audioCh, videoCh, logger); err != nil {
			errCh <- fmt.Errorf("audio/video split failed: %w", err)
		}
	}()

	// Process audio for transcription
	var wg sync.WaitGroup
	wg.Add(1)

	go func() {
		defer wg.Done()
		if err := p.processAudio(audioCh, videoCh, resultCh, srcLang, targetLang, logger); err != nil {
			errCh <- fmt.Errorf("audio processing failed: %w", err)
		}
	}()

	// Stream to targets
	go func() {
		wg.Wait()
		close(resultCh)
	}()

	streamer := streaming.New(targets)
	streamErrors := 0

	for {
		select {
		case processedData, ok := <-resultCh:
			if !ok {
				// Channel closed, all processing complete
				if streamErrors > 0 {
					return fmt.Errorf("%d streaming errors occurred", streamErrors)
				}
				return nil
			}

			if err := streamer.Stream(processedData); err != nil {
				streamErrors++
				logger.WithError(err).Error("Error streaming to targets")
			}

		case err := <-errCh:
			return err
		}
	}
}

func (p *Proxy) splitAudioVideo(inputPath string, audioCh, videoCh chan<- []byte, logger *logrus.Entry) error {
	tempDir := filepath.Dir(inputPath)

	// Extract audio using FFmpeg
	audioPath := filepath.Join(tempDir, "audio.wav")
	audioArgs := []string{
		"-i", inputPath,
		"-vn",                  // Skip video
		"-acodec", "pcm_s16le", // Use PCM 16-bit audio codec
		"-ar", "16000", // Set sample rate to 16kHz
		"-ac", "1", // Convert to mono
		"-y", // Overwrite output file
		audioPath,
	}

	logger.Info("Extracting audio from input stream")
	audioCmd := exec.Command("ffmpeg", audioArgs...)
	audioOutput, err := audioCmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to extract audio: %w, output: %s", err, string(audioOutput))
	}

	// Read the audio file
	audioData, err := os.ReadFile(audioPath)
	if err != nil {
		return fmt.Errorf("failed to read audio file: %w", err)
	}
	audioCh <- audioData

	// Copy the original video for later processing
	videoPath := filepath.Join(tempDir, "video.mp4")
	videoArgs := []string{
		"-i", inputPath,
		"-c", "copy", // Copy all streams
		"-y", // Overwrite output file
		videoPath,
	}

	logger.Info("Preparing video for processing")
	videoCmd := exec.Command("ffmpeg", videoArgs...)
	videoOutput, err := videoCmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to prepare video: %w, output: %s", err, string(videoOutput))
	}

	// Read the video file
	videoData, err := os.ReadFile(videoPath)
	if err != nil {
		return fmt.Errorf("failed to read video file: %w", err)
	}
	videoCh <- videoData

	return nil
}

func (p *Proxy) processAudio(audioCh <-chan []byte, videoCh <-chan []byte, resultCh chan<- []byte, srcLang, targetLang string, logger *logrus.Entry) error {
	var audioData []byte
	for chunk := range audioCh {
		audioData = append(audioData, chunk...)
	}

	var videoData []byte
	for chunk := range videoCh {
		videoData = append(videoData, chunk...)
	}

	// Process transcription
	startTime := time.Now()
	logger.Info("Starting transcription")

	segments, err := p.transcriber.TranscribeAudio(audioData, srcLang)
	if err != nil {
		logger.WithError(err).Error("Transcription failed")
		// If transcription fails, forward the original video
		resultCh <- videoData
		return err
	}

	logger.WithField("duration", time.Since(startTime)).WithField("segments", len(segments)).Info("Transcription completed")

	// Translate if target language is specified
	if targetLang != "" && targetLang != srcLang {
		logger.WithFields(logrus.Fields{
			"source": srcLang,
			"target": targetLang,
		}).Info("Starting translation with Argos Translate")

		translationStart := time.Now()
		translatedSegments, err := p.translator.TranslateSegments(segments, srcLang, targetLang)
		if err != nil {
			logger.WithError(err).Error("Translation failed, using original transcription")
		} else {
			segments = translatedSegments
			logger.WithField("duration", time.Since(translationStart)).
				Info("Translation completed")
		}
	}

	// Embed subtitles
	logger.Info("Embedding subtitles into video stream")
	processedVideo, err := p.embedder.EmbedSubtitles(videoData, segments)
	if err != nil {
		logger.WithError(err).Error("Failed to embed subtitles, using original video")
		resultCh <- videoData
		return err
	}

	resultCh <- processedVideo
	logger.Info("Video processing completed")

	return nil
}

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
