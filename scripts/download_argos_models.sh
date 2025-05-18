#!/bin/bash
# Script to download Argos Translate language models

set -e

MODELS_DIR=${1:-/app/models/argos}

# Create model directory if it doesn't exist
mkdir -p "$MODELS_DIR"
cd "$MODELS_DIR"

echo "Downloading Argos Translate language models to $MODELS_DIR..."

# Define common language pairs to download
LANGUAGE_PAIRS=(
  "en_fr"
  "en_de"
  "en_es"
  "en_it"
  "en_pt"
  "en_ru"
  "en_zh"
  "en_ja"
  "fr_en"
  "de_en"
  "es_en"
  "it_en"
  "pt_en"
  "ru_en"
  "zh_en"
  "ja_en"
)

# Download and install models using argospm
for pair in "${LANGUAGE_PAIRS[@]}"; do
  src="${pair%_*}"
  tgt="${pair#*_}"
  echo "Downloading $src to $tgt translation model..."
  argospm install "translate-$src-to-$tgt" || echo "Failed to download $src to $tgt model, skipping..."
done

echo "Argos Translate language models download completed!"

# Create a simple script to list installed packages
echo "#!/bin/bash
argospm list
" > "$MODELS_DIR/list_models.sh"
chmod +x "$MODELS_DIR/list_models.sh"

# List installed packages
echo "Installed Argos Translate packages:"
"$MODELS_DIR/list_models.sh"