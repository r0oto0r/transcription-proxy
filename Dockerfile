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

# Install dependencies for whisper-ctranslate2 and Argos Translate
RUN apt-get update && apt-get install -y \
    python3 \
    python3-pip \
    python3-dev \
    ffmpeg \
    libsm6 \
    libxext6 \
    curl \
    git \
    build-essential \
    && rm -rf /var/lib/apt/lists/*

# Install CUDA optimized PyTorch and dependencies
RUN pip3 install --no-cache-dir --upgrade pip

# Install whisper-ctranslate2
RUN pip3 install --no-cache-dir whisper-ctranslate2

# Install Argos Translate and dependencies
RUN pip3 install --no-cache-dir argostranslate

# Set up model directories with proper permissions
RUN mkdir -p /app/models/whisper /app/models/argos /app/transcripts && \
    chmod 777 /app/models/whisper /app/models/argos /app/transcripts

# Copy the application and scripts
COPY --from=build /app/bin/transcription-proxy /app/
COPY --from=build /app/scripts/download_model.sh /app/
COPY --from=build /app/scripts/download_argos_models.sh /app/
COPY --from=build /app/entrypoint.sh /app/
RUN chmod +x /app/download_model.sh /app/download_argos_models.sh /app/entrypoint.sh

# Expose ports
EXPOSE 8080 1935 1936

# Set working directory
WORKDIR /app

ENTRYPOINT ["/app/entrypoint.sh"]