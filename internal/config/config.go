package config

import (
	"os"
	"strconv"
)

type Config struct {
	// Server settings
	ListenAddress string
	OutputDir     string
	LogLevel      string

	// RTMP settings
	RTMPPort          string
	DefaultTargetURL  string
	DefaultSourceLang string
	DefaultTargetLang string

	// Whisper model settings
	WhisperModelPath string
	WhisperModelSize string
	CUDAEnabled      bool
	MaxVRAMUsageMB   int
	ComputePrecision string
	BatchSize        int
	BeamSize         int
	GPUThreads       int

	// Argos Translate settings
	ArgosModelsPath   string
	EnableTranslation bool
	ArgosVRAMUsageMB  int
}

func New() *Config {
	return &Config{
		// Server settings
		ListenAddress: getEnvOrDefault("LISTEN_ADDRESS", ":8080"),
		OutputDir:     getEnvOrDefault("OUTPUT_DIR", "/app/transcripts"),
		LogLevel:      getEnvOrDefault("LOG_LEVEL", "info"),

		// RTMP settings
		RTMPPort:          getEnvOrDefault("RTMP_PORT", "1935"),
		DefaultTargetURL:  getEnvOrDefault("TARGET_URL", "rtmp://localhost:1936/out"),
		DefaultSourceLang: getEnvOrDefault("SRC_LANG", "en"),
		DefaultTargetLang: getEnvOrDefault("LANG", "en"),

		// Whisper model settings
		WhisperModelPath: getEnvOrDefault("WHISPER_MODEL_PATH", "/app/models/whisper"),
		WhisperModelSize: getEnvOrDefault("WHISPER_MODEL_SIZE", "large-v3"),
		CUDAEnabled:      getEnvBoolOrDefault("CUDA_ENABLED", true),
		MaxVRAMUsageMB:   getEnvIntOrDefault("MAX_VRAM_USAGE_MB", 8000),
		ComputePrecision: getEnvOrDefault("COMPUTE_PRECISION", "float16"),
		BatchSize:        getEnvIntOrDefault("BATCH_SIZE", 16),
		BeamSize:         getEnvIntOrDefault("BEAM_SIZE", 5),
		GPUThreads:       getEnvIntOrDefault("GPU_THREADS", 4),

		// Argos Translate settings
		ArgosModelsPath:   getEnvOrDefault("ARGOS_MODELS_PATH", "/app/models/argos"),
		EnableTranslation: getEnvBoolOrDefault("ENABLE_TRANSLATION", true),
		ArgosVRAMUsageMB:  getEnvIntOrDefault("ARGOS_VRAM_USAGE_MB", 4000),
	}
}

func getEnvOrDefault(key, defaultValue string) string {
	if value, exists := os.LookupEnv(key); exists {
		return value
	}
	return defaultValue
}

func getEnvBoolOrDefault(key string, defaultValue bool) bool {
	if value, exists := os.LookupEnv(key); exists {
		if boolValue, err := strconv.ParseBool(value); err == nil {
			return boolValue
		}
	}
	return defaultValue
}

func getEnvIntOrDefault(key string, defaultValue int) int {
	if value, exists := os.LookupEnv(key); exists {
		if intValue, err := strconv.Atoi(value); err == nil {
			return intValue
		}
	}
	return defaultValue
}
