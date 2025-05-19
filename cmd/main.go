package main

import (
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/ben/transcription-proxy/internal/config"
	"github.com/ben/transcription-proxy/internal/proxy"
)

func main() {
	cfg := config.New()

	log.Printf("Starting transcription RTMP server with configuration:")
	log.Printf("RTMP port: %s", cfg.RTMPPort)
	log.Printf("CUDA enabled: %v", cfg.CUDAEnabled)
	log.Printf("Model path: %s", cfg.WhisperModelPath)
	log.Printf("Model size: %s", cfg.WhisperModelSize)
	log.Printf("Output directory: %s", cfg.OutputDir)
	log.Printf("Default target URL: %s", cfg.DefaultTargetURL)
	log.Printf("Log level: %s", cfg.LogLevel)

	// Initialize and start the RTMP server
	proxyServer := proxy.New(cfg)

	// Start the server in a non-blocking way
	if err := proxyServer.Start(); err != nil {
		log.Fatalf("Failed to start RTMP server: %v", err)
	}

	log.Println("RTMP server started successfully and listening for connections")

	// Handle graceful shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	// Wait for termination signal
	sig := <-sigCh
	log.Printf("Received signal %v, shutting down...", sig)

	// Clean shutdown of the RTMP server
	if err := proxyServer.Stop(); err != nil {
		log.Printf("Error stopping RTMP server: %v", err)
	}

	log.Println("Server shutdown complete")
}
