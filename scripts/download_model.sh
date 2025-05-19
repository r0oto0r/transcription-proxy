#!/bin/bash
# Script to download the deepdml/faster-whisper-large-v3-turbo-ct2 model from Hugging Face

set -e

MODEL_NAME="deepdml/faster-whisper-large-v3-turbo-ct2"
MODEL_DIR=${1:-/app/models/whisper}
COMPUTE_TYPE=${2:-float16}
USE_CUDA=${3:-true}

echo "Downloading $MODEL_NAME from Hugging Face..."
echo "Using compute type: $COMPUTE_TYPE"
echo "Model directory: $MODEL_DIR"
echo "Using CUDA: $USE_CUDA"

# Create model directory if it doesn't exist
mkdir -p "$MODEL_DIR"

# Use huggingface_hub to download the model
python3 -c "
import os
import sys

# Parameters
model_name = '$MODEL_NAME'
model_dir = '$MODEL_DIR'
compute_type = '$COMPUTE_TYPE'
use_cuda = '$USE_CUDA'.lower() == 'true'

# Ensure the model directory exists
os.makedirs(model_dir, exist_ok=True)

# Import the necessary modules
try:
    from huggingface_hub import snapshot_download
except ImportError as e:
    print(f'Error importing modules: {e}')
    print('Installing required packages...')
    import subprocess
    subprocess.check_call([sys.executable, '-m', 'pip', 'install', 'huggingface_hub'])
    from huggingface_hub import snapshot_download

print(f'Downloading {model_name} model from Hugging Face...')
try:
    # Download the model from Hugging Face
    model_path = snapshot_download(
        repo_id=model_name,
        local_dir=os.path.join(model_dir, 'faster-whisper-large-v3-turbo-ct2'),
        local_dir_use_symlinks=False
    )
    
    print(f'Model successfully downloaded to: {model_path}')
except Exception as e:
    print(f'Error downloading model: {e}')
    sys.exit(1)
"

echo "Model download completed!"