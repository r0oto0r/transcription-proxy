package translator

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"

	"github.com/ben/transcription-proxy/internal/config"
	"github.com/ben/transcription-proxy/internal/transcriber"
)

// Translator handles text translation using Argos Translate
type Translator struct {
	config       *config.Config
	modelsPath   string
	langPairLock sync.Mutex
	loadedPairs  map[string]bool
}

// New creates a new Translator instance
func New(cfg *config.Config) *Translator {
	return &Translator{
		config:      cfg,
		modelsPath:  cfg.ArgosModelsPath,
		loadedPairs: make(map[string]bool),
	}
}

// TranslateSegments translates an array of transcript segments to the target language
func (t *Translator) TranslateSegments(segments []transcriber.Segment, sourceLang, targetLang string) ([]transcriber.Segment, error) {
	if !t.config.EnableTranslation {
		return segments, nil
	}

	// Skip if source and target languages are the same
	if sourceLang == targetLang {
		return segments, nil
	}

	// Normalize language codes
	sourceLang = normalizeLanguageCode(sourceLang)
	targetLang = normalizeLanguageCode(targetLang)

	// Check if we have the required language pair
	if !t.checkLanguagePair(sourceLang, targetLang) {
		return segments, fmt.Errorf("translation model for %s to %s not available", sourceLang, targetLang)
	}

	// Prepare translated segments
	translatedSegments := make([]transcriber.Segment, len(segments))
	for i, segment := range segments {
		// Copy segment properties
		translatedSegments[i] = segment

		// Translate text
		translatedText, err := t.translateText(segment.Text, sourceLang, targetLang)
		if err != nil {
			// On error, keep original text but log the error (in a real implementation)
			translatedSegments[i].Text = segment.Text
			continue
		}

		translatedSegments[i].Text = translatedText
	}

	return translatedSegments, nil
}

// translateText translates a single string from source to target language
func (t *Translator) translateText(text, sourceLang, targetLang string) (string, error) {
	// Create command with pipes
	cmd := exec.Command("argos-translate", "--from", sourceLang, "--to", targetLang, "-")
	cmd.Env = append(os.Environ(), fmt.Sprintf("ARGOS_PACKAGES_DIR=%s", t.modelsPath))

	// Create input and output pipes
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return "", fmt.Errorf("failed to create stdin pipe: %w", err)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", fmt.Errorf("failed to create stdout pipe: %w", err)
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		return "", fmt.Errorf("failed to create stderr pipe: %w", err)
	}

	// Start the command
	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("failed to start argos-translate: %w", err)
	}

	// Write text to stdin
	go func() {
		defer stdin.Close()
		if _, err := io.WriteString(stdin, text); err != nil {
			fmt.Fprintf(os.Stderr, "error writing text to argos-translate: %v\n", err)
		}
	}()

	// Read output from stdout
	var output bytes.Buffer
	if _, err := io.Copy(&output, stdout); err != nil {
		return "", fmt.Errorf("failed to read translation: %w", err)
	}

	// Read stderr for error messages
	var stderrOutput bytes.Buffer
	if _, err := io.Copy(&stderrOutput, stderr); err != nil {
		return "", fmt.Errorf("failed to read stderr: %w", err)
	}

	// Wait for command to complete
	err = cmd.Wait()
	if err != nil {
		return "", fmt.Errorf("translation failed: %w, stderr: %s", err, stderrOutput.String())
	}

	// Return the translated text
	return strings.TrimSpace(output.String()), nil
}

// checkLanguagePair verifies if the language pair is available
func (t *Translator) checkLanguagePair(sourceLang, targetLang string) bool {
	// Generate a unique key for this language pair
	pairKey := fmt.Sprintf("%s-%s", sourceLang, targetLang)

	// Check if we've already verified this pair
	t.langPairLock.Lock()
	defer t.langPairLock.Unlock()

	if loaded, exists := t.loadedPairs[pairKey]; exists {
		return loaded
	}

	// Check if the language pair is available by listing installed packages
	cmd := exec.Command("argospm", "list")
	cmd.Env = append(os.Environ(), fmt.Sprintf("ARGOS_PACKAGES_DIR=%s", t.modelsPath))

	output, err := cmd.CombinedOutput()
	if err != nil {
		return false
	}

	// Look for the required package in the output
	packageName := fmt.Sprintf("translate-%s-to-%s", sourceLang, targetLang)
	if strings.Contains(string(output), packageName) {
		t.loadedPairs[pairKey] = true
		return true
	}

	t.loadedPairs[pairKey] = false
	return false
}

// normalizeLanguageCode converts language codes to the format used by Argos Translate
func normalizeLanguageCode(lang string) string {
	// Map common codes to Argos Translate's codes
	langMap := map[string]string{
		"en-us": "en",
		"en-gb": "en",
		"de-de": "de",
		"fr-fr": "fr",
		"es-es": "es",
		"it-it": "it",
		"pt-br": "pt",
		"pt-pt": "pt",
		"ru-ru": "ru",
		"zh-cn": "zh",
		"zh-tw": "zh",
		"ja-jp": "ja",
		"ko-kr": "ko",
	}

	// Convert to lowercase for consistency
	lang = strings.ToLower(lang)

	// Check if we have a specific mapping
	if mapped, exists := langMap[lang]; exists {
		return mapped
	}

	// If it's a BCP 47 code (like en-US), take just the language part
	if strings.Contains(lang, "-") {
		return strings.Split(lang, "-")[0]
	}

	return lang
}
