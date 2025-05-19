#!/bin/bash

# Check if we need to download the whisper model
if [ ! -d "/app/models/whisper/${WHISPER_MODEL_SIZE}" ]; then
  echo "Model directory: ${WHISPER_MODEL_PATH}"
  echo "Downloading and optimizing ${WHISPER_MODEL_SIZE} model..."
  /app/download_model.sh "${WHISPER_MODEL_SIZE}" "${WHISPER_MODEL_PATH}" "${COMPUTE_PRECISION}" "${CUDA_ENABLED}"
fi

# Download Argos models for supported languages
/app/download_argos_models.sh "/app/models/argos"

# Run the transcription proxy
echo "Starting transcription proxy RTMP server..."
exec /app/transcription-proxy