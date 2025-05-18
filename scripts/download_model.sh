#!/bin/bash
# Script to download and optimize models for faster-whisper

set -e

MODEL_SIZE=${1:-large-v3}
MODEL_DIR=${2:-/app/models/whisper}
COMPUTE_TYPE=${3:-float16}

echo "Downloading and optimizing the $MODEL_SIZE model for faster-whisper..."
echo "Using compute type: $COMPUTE_TYPE"
echo "Model directory: $MODEL_DIR"

# Create model directory if it doesn't exist
mkdir -p "$MODEL_DIR"

# Use Python to download and optimize the model
python3 -c "
import os
import sys
from faster_whisper import WhisperModel
from ctranslate2.converters import TransformersConverter

# Parameters
model_size = '$MODEL_SIZE'
model_dir = '$MODEL_DIR'
compute_type = '$COMPUTE_TYPE'
model_path = os.path.join(model_dir, model_size)

# Download and optimize the model
print(f'Downloading and optimizing {model_size} model...')
try:
    # Initialize the model with optimized settings for 16GB VRAM
    model = WhisperModel(
        model_size,
        device='cuda',
        compute_type=compute_type,
        download_root=model_dir,
        cpu_threads=4,
        num_workers=2
    )
    print(f'Model {model_size} successfully downloaded and optimized with {compute_type} precision')
    print(f'Model path: {model_path}')
except Exception as e:
    print(f'Error downloading or loading model: {e}')
    sys.exit(1)
"

echo "Model download and optimization completed!"