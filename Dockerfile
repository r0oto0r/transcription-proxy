FROM nvidia/cuda:12.9.0-runtime-ubuntu22.04 AS build

# Install Go
RUN apt-get update && apt-get install -y \
    wget \
    git \
    build-essential \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /tmp
RUN wget https://go.dev/dl/go1.22.0.linux-amd64.tar.gz && \
    tar -C /usr/local -xzf go1.22.0.linux-amd64.tar.gz
ENV PATH=$PATH:/usr/local/go/bin

# Set up Go workspace
WORKDIR /app
COPY go.mod .
COPY go.sum .
RUN go mod download

# Copy source code
COPY . .

# Build the application
RUN go build -o /app/bin/transcription-proxy ./cmd

FROM nvidia/cuda:12.9.0-runtime-ubuntu22.04

# Install dependencies for faster-whisper and Argos Translate
RUN apt-get update && apt-get install -y \
    python3 \
    python3-pip \
    python3-dev \
    ffmpeg \
    libsm6 \
    libxext6 \
    curl \
    git \
    && rm -rf /var/lib/apt/lists/*

# Install CUDA optimized PyTorch and dependencies
RUN pip3 install --no-cache-dir --upgrade pip

# Install latest faster-whisper
RUN pip3 install --no-cache-dir faster-whisper

# Install Argos Translate and dependencies
RUN pip3 install --no-cache-dir argostranslate sentencepiece protobuf
RUN pip3 install --no-cache-dir argospm

# Download optimization tools for GPU performance
RUN pip3 install --no-cache-dir nvidia-ml-py3 gputil

# Set up model directories with proper permissions
RUN mkdir -p /app/models/whisper /app/models/argos /app/transcripts && \
    chmod 777 /app/models/whisper /app/models/argos /app/transcripts

# Copy the application and scripts
COPY --from=build /app/bin/transcription-proxy /app/
COPY --from=build /app/scripts/download_model.sh /app/
COPY --from=build /app/scripts/download_argos_models.sh /app/
RUN chmod +x /app/download_model.sh /app/download_argos_models.sh

# Expose port
EXPOSE 8080

# Set working directory
WORKDIR /app

# Create and set entrypoint script
RUN echo '#!/bin/bash\n\
# Check if we need to download the whisper model\n\
if [ ! -d "/app/models/whisper/${WHISPER_MODEL_SIZE}" ]; then\n\
  echo "Whisper model ${WHISPER_MODEL_SIZE} not found, downloading..."\n\
  /app/download_model.sh "${WHISPER_MODEL_SIZE}" "/app/models/whisper" "${COMPUTE_PRECISION}"\n\
fi\n\
\n\
# Download Argos models for supported languages\n\
/app/download_argos_models.sh "/app/models/argos"\n\
\n\
# Run the application\n\
exec /app/transcription-proxy\n\
' > /app/entrypoint.sh && chmod +x /app/entrypoint.sh

ENTRYPOINT ["/app/entrypoint.sh"]