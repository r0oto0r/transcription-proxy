#!/bin/bash
# run.sh - Convenience script to start the transcription-proxy in either GPU or CPU mode

# Default to CPU mode if no argument is provided
MODE=${1:-cpu}

case "$MODE" in
  "gpu")
    echo "Starting transcription-proxy in GPU mode..."
    if ! command -v nvidia-smi &> /dev/null; then
      echo "Warning: nvidia-smi command not found. Are you sure NVIDIA drivers are installed?"
      echo "Checking for NVIDIA runtime..."
      if ! docker info | grep -q "Runtimes:.*nvidia"; then
        echo "Error: NVIDIA Docker runtime not found. Please install nvidia-docker2."
        echo "See: https://docs.nvidia.com/datacenter/cloud-native/container-toolkit/install-guide.html"
        exit 1
      fi
    fi
    docker compose --profile gpu up -d transcription-proxy-gpu
    ;;
  "cpu")
    echo "Starting transcription-proxy in CPU mode..."
    docker compose --profile cpu up -d transcription-proxy-cpu
    ;;
  *)
    echo "Invalid mode: $MODE"
    echo "Usage: $0 [cpu|gpu]"
    echo "  cpu - Start in CPU mode (default)"
    echo "  gpu - Start in GPU mode (requires NVIDIA GPU and drivers)"
    exit 1
    ;;
esac

echo "Transcription proxy is starting. Check logs with:"
echo "  docker compose logs -f transcription-proxy-$MODE"
echo ""
echo "Service will be available at: http://localhost:8080"
echo "To stop the service: docker compose stop transcription-proxy-$MODE"