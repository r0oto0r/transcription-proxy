package streaming

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

type StreamType string

const (
	StreamTypeTwitch  StreamType = "twitch"
	StreamTypeYouTube StreamType = "youtube"
	StreamTypeCustom  StreamType = "custom"
)

type StreamTarget struct {
	URL       string
	Type      StreamType
	StreamKey string
	AuthToken string
}

func ParseStreamURL(inputURL string) (*StreamTarget, error) {
	parsedURL, err := url.Parse(inputURL)
	if err != nil {
		return nil, fmt.Errorf("invalid URL: %w", err)
	}

	path := strings.TrimPrefix(parsedURL.Path, "/")
	pathParts := strings.Split(path, "/")

	if len(pathParts) < 2 {
		return nil, errors.New("URL path must include streaming type and stream key")
	}

	streamType := getStreamType(pathParts[0])
	streamKey := pathParts[1]

	query := parsedURL.Query()
	authToken := query.Get("auth")

	var targetURL string

	switch streamType {
	case StreamTypeTwitch:
		targetURL = fmt.Sprintf("rtmp://live.twitch.tv/app/%s", streamKey)
	case StreamTypeYouTube:
		targetURL = fmt.Sprintf("rtmp://a.rtmp.youtube.com/live2/%s", streamKey)
	case StreamTypeCustom:
		targetURL = fmt.Sprintf("%s://%s%s", parsedURL.Scheme, parsedURL.Host, parsedURL.Path)
	}

	return &StreamTarget{
		URL:       targetURL,
		Type:      streamType,
		StreamKey: streamKey,
		AuthToken: authToken,
	}, nil
}

func getStreamType(typeStr string) StreamType {
	switch strings.ToLower(typeStr) {
	case "twitch":
		return StreamTypeTwitch
	case "youtube":
		return StreamTypeYouTube
	default:
		return StreamTypeCustom
	}
}

type Streamer struct {
	targets []*StreamTarget
}

func New(targets []*StreamTarget) *Streamer {
	return &Streamer{
		targets: targets,
	}
}

func (s *Streamer) Stream(data []byte) error {
	if len(s.targets) == 0 {
		return errors.New("no streaming targets specified")
	}

	// Create temporary file for the input video
	tempDir, err := os.MkdirTemp("", "streaming")
	if err != nil {
		return fmt.Errorf("failed to create temp directory: %w", err)
	}
	defer os.RemoveAll(tempDir)

	inputPath := filepath.Join(tempDir, "input.mp4")
	if err := os.WriteFile(inputPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write video to temp file: %w", err)
	}

	// Stream to all targets concurrently
	var wg sync.WaitGroup
	errCh := make(chan error, len(s.targets))

	for _, target := range s.targets {
		wg.Add(1)

		go func(target *StreamTarget) {
			defer wg.Done()

			if err := s.streamToTarget(inputPath, target); err != nil {
				errCh <- fmt.Errorf("streaming to %s failed: %w", target.Type, err)
			}
		}(target)
	}

	// Wait for all streaming goroutines to complete
	wg.Wait()
	close(errCh)

	// Collect any errors
	var errs []string
	for err := range errCh {
		errs = append(errs, err.Error())
	}

	if len(errs) > 0 {
		return fmt.Errorf("streaming errors: %s", strings.Join(errs, "; "))
	}

	return nil
}

func (s *Streamer) streamToTarget(inputPath string, target *StreamTarget) error {
	// Construct FFmpeg command to stream to the target
	args := []string{
		"-re",           // Read input at native frame rate
		"-i", inputPath, // Input file
		"-c:v", "copy", // Copy video codec
		"-c:a", "copy", // Copy audio codec
		"-c:s", "copy", // Copy subtitles
		"-f", "flv", // Output format (FLV for RTMP)
	}

	// Add authentication if provided
	if target.AuthToken != "" {
		switch target.Type {
		case StreamTypeTwitch:
			// For Twitch, the auth token is typically included in the URL
			args = append(args, fmt.Sprintf("%s?auth=%s", target.URL, target.AuthToken))
		case StreamTypeYouTube:
			// For YouTube, the auth token might be handled differently
			args = append(args, fmt.Sprintf("%s?auth=%s", target.URL, target.AuthToken))
		default:
			args = append(args, target.URL)
		}
	} else {
		args = append(args, target.URL)
	}

	cmd := exec.Command("ffmpeg", args...)

	// Capture stdout/stderr
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("ffmpeg streaming failed: %w, output: %s", err, string(output))
	}

	return nil
}
