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

	log.Printf("Starting transcription proxy with configuration:")
	log.Printf("Listen address: %s", cfg.ListenAddress)
	log.Printf("CUDA enabled: %v", cfg.CUDAEnabled)
	log.Printf("Model path: %s", cfg.WhisperModelPath)
	log.Printf("Model size: %s", cfg.WhisperModelSize)
	log.Printf("Output directory: %s", cfg.OutputDir)
	log.Printf("Log level: %s", cfg.LogLevel)

	proxyServer := proxy.New(cfg)

	// Handle graceful shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		sig := <-sigCh
		log.Printf("Received signal %v, shutting down...", sig)
		// Perform cleanup here if needed
		os.Exit(0)
	}()

	log.Fatal(proxyServer.Start())
}
