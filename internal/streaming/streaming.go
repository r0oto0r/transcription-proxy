package streaming

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
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
	targets              []*StreamTarget
	persistentCmds       map[*StreamTarget]*exec.Cmd
	persistentStdinPipes map[*StreamTarget]io.WriteCloser
	mu                   sync.Mutex // Mutex to protect the maps
	initialized          bool
}

func New(targets []*StreamTarget) *Streamer {
	return &Streamer{
		targets:              targets,
		persistentCmds:       make(map[*StreamTarget]*exec.Cmd),
		persistentStdinPipes: make(map[*StreamTarget]io.WriteCloser),
	}
}

// Initialize sets up persistent FFmpeg processes for all targets
func (s *Streamer) Initialize() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.initialized {
		return nil // Already initialized
	}

	if len(s.targets) == 0 {
		return errors.New("no streaming targets specified")
	}

	var initErrors []string

	for _, target := range s.targets {
		if err := s.initializeTarget(target); err != nil {
			initErrors = append(initErrors, fmt.Sprintf("%s: %v", target.Type, err))
		}
	}

	if len(initErrors) > 0 {
		// Clean up any successful initializations
		s.Cleanup()
		return fmt.Errorf("initialization errors: %s", strings.Join(initErrors, "; "))
	}

	s.initialized = true
	return nil
}

func (s *Streamer) initializeTarget(target *StreamTarget) error {
	// Construct FFmpeg command to stream to the target in a persistent mode
	args := []string{
		"-fflags", "nobuffer", // Reduce latency
		"-re",          // Read input at native frame rate
		"-i", "pipe:0", // Read from stdin without specifying format
		"-c:v", "copy", // Copy video codec
		"-c:a", "copy", // Copy audio codec
		"-c:s", "copy", // Copy subtitles
		"-f", "flv", // Output format (FLV for RTMP)
	}

	// Add authentication if provided
	outputURL := target.URL
	if target.AuthToken != "" {
		switch target.Type {
		case StreamTypeTwitch, StreamTypeYouTube:
			outputURL = fmt.Sprintf("%s?auth=%s", target.URL, target.AuthToken)
		}
	}
	args = append(args, outputURL)

	cmd := exec.Command("ffmpeg", args...)

	// Create stdin pipe to send video data
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("failed to create stdin pipe: %w", err)
	}

	// Redirect stderr to a file or logger rather than buffering it all in memory
	cmd.Stderr = os.Stderr

	// Start the command
	if err := cmd.Start(); err != nil {
		stdin.Close()
		return fmt.Errorf("failed to start ffmpeg: %w", err)
	}

	// Store the command and stdin pipe for this target
	s.persistentCmds[target] = cmd
	s.persistentStdinPipes[target] = stdin

	return nil
}

// Stream sends a chunk of video data to all initialized streaming targets
func (s *Streamer) Stream(data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.initialized {
		if err := s.Initialize(); err != nil {
			return fmt.Errorf("failed to initialize streaming: %w", err)
		}
	}

	var streamErrors []string

	// Send data to all targets concurrently
	var wg sync.WaitGroup
	errCh := make(chan error, len(s.targets))

	for _, target := range s.targets {
		wg.Add(1)

		go func(target *StreamTarget) {
			defer wg.Done()

			pipe, ok := s.persistentStdinPipes[target]
			if !ok {
				errCh <- fmt.Errorf("no stdin pipe for target %s", target.Type)
				return
			}

			if _, err := pipe.Write(data); err != nil {
				errCh <- fmt.Errorf("error writing to target %s: %w", target.Type, err)

				// Try to reinitialize this target
				s.mu.Lock()
				s.cleanupTarget(target)
				if err := s.initializeTarget(target); err != nil {
					errCh <- fmt.Errorf("failed to reinitialize target %s: %w", target.Type, err)
				}
				s.mu.Unlock()
			}
		}(target)
	}

	// Wait for all writing goroutines to complete
	wg.Wait()
	close(errCh)

	// Collect any errors
	for err := range errCh {
		streamErrors = append(streamErrors, err.Error())
	}

	if len(streamErrors) > 0 {
		return fmt.Errorf("streaming errors: %s", strings.Join(streamErrors, "; "))
	}

	return nil
}

// Cleanup closes all persistent FFmpeg processes
func (s *Streamer) Cleanup() {
	s.mu.Lock()
	defer s.mu.Unlock()

	for target, pipe := range s.persistentStdinPipes {
		pipe.Close()
		delete(s.persistentStdinPipes, target)
	}

	for target, cmd := range s.persistentCmds {
		if cmd.Process != nil {
			cmd.Process.Signal(os.Interrupt)
			cmd.Wait() // Wait for the process to exit
		}
		delete(s.persistentCmds, target)
	}

	s.initialized = false
}

func (s *Streamer) cleanupTarget(target *StreamTarget) {
	if pipe, ok := s.persistentStdinPipes[target]; ok {
		pipe.Close()
		delete(s.persistentStdinPipes, target)
	}

	if cmd, ok := s.persistentCmds[target]; ok {
		if cmd.Process != nil {
			cmd.Process.Signal(os.Interrupt)
			cmd.Wait() // Wait for the process to exit
		}
		delete(s.persistentCmds, target)
	}
}

// streamToTarget is kept for backward compatibility but is deprecated
func (s *Streamer) streamToTarget(data []byte, target *StreamTarget) error {
	// Construct FFmpeg command to stream to the target
	args := []string{
		"-re",       // Read input at native frame rate
		"-f", "mp4", // Specify input format for the pipe
		"-i", "pipe:0", // Read from stdin
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

	// Create stdin pipe to send video data
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("failed to create stdin pipe: %w", err)
	}

	// Create buffer for stderr output
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	// Start the command
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start ffmpeg: %w", err)
	}

	// Write video data to stdin
	go func() {
		defer stdin.Close()
		if _, err := stdin.Write(data); err != nil {
			// Silently handle the error but don't print to stderr
			_ = err
		}
	}()

	// Wait for the command to complete
	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("ffmpeg streaming failed: %w, output: %s", err, stderr.String())
	}

	return nil
}
