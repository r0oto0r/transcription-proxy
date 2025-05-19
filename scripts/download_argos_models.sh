#!/bin/bash
# Script to download Argos Translate language models with parallel downloading

set -e

MODELS_DIR=${1:-/app/models/argos}
MAX_PARALLEL=${2:-4}  # Maximum number of parallel downloads
SPECIFIED_PAIRS=${@:3}  # Optional specific language pairs to download

# Create model directory if it doesn't exist
mkdir -p "$MODELS_DIR"
cd "$MODELS_DIR"

echo "Downloading Argos Translate language models to $MODELS_DIR..."
echo "Using max $MAX_PARALLEL parallel downloads"

# Define common language pairs to download
DEFAULT_LANGUAGE_PAIRS=(
  "en-fr"
  "en-de"
  "en-es"
  "en-it"
  "en-pt"
  "en-ru"
  "en-zh"
  "en-ja"
  "fr-en"
  "de-en"
  "es-en"
  "it-en"
  "pt-en"
  "ru-en"
  "zh-en"
  "ja-en"
)

# Use specified pairs if provided, otherwise use default
if [ ${#SPECIFIED_PAIRS[@]} -gt 0 ]; then
  LANGUAGE_PAIRS=("${SPECIFIED_PAIRS[@]}")
  echo "Downloading specified language pairs: ${LANGUAGE_PAIRS[*]}"
else
  LANGUAGE_PAIRS=("${DEFAULT_LANGUAGE_PAIRS[@]}")
  echo "Downloading all default language pairs"
fi

# Function to download a single language pair
download_pair() {
  local pair=$1
  local src="${pair%-*}"
  local tgt="${pair#*-}"
  local log_file="${MODELS_DIR}/${src}-${tgt}.log"
  
  echo "Starting download of $src to $tgt translation model..."
  
  # Try Python API first
  python3 -c "
import sys
import os
import argostranslate.package
import argostranslate.translate

def download_package(from_code, to_code):
    print(f\"Downloading {from_code} to {to_code} translation model...\")
    try:
        # Update package index
        argostranslate.package.update_package_index()
        
        # Get available packages
        available_packages = argostranslate.package.get_available_packages()
        
        # Find the right package
        package_to_install = None
        for package in available_packages:
            if package.from_code == from_code and package.to_code == to_code:
                package_to_install = package
                break
        
        if package_to_install is None:
            print(f\"Package for {from_code} to {to_code} not found in index\")
            return False
            
        # Download and install the package
        print(f\"Found {from_code}-{to_code} package, size: {package_to_install.package_size/1024/1024:.1f}MB\")
        package_to_install.install()
        print(f\"Successfully installed {from_code} to {to_code} model\")
        return True
    except Exception as e:
        print(f\"Error installing {from_code} to {to_code} model: {str(e)}\")
        return False

if not download_package('$src', '$tgt'):
    # Try argospm as a fallback
    import subprocess
    try:
        print(f\"Trying argospm for {src}-{tgt}\")
        subprocess.check_call(['argospm', 'install', f'translate-{src}-to-{tgt}'])
        print(f\"Successfully installed {src}-{tgt} with argospm\")
    except:
        try:
            subprocess.check_call(['argospm', 'install', f'{src}-{tgt}'])
            print(f\"Successfully installed {src}-{tgt} with alternate argospm syntax\")
        except:
            print(f\"Failed to install {src}-{tgt} with all methods\")
            sys.exit(1)
" > "$log_file" 2>&1
  
  local status=$?
  if [ $status -eq 0 ]; then
    echo "✅ Completed $src to $tgt download"
  else
    echo "❌ Failed $src to $tgt download (see $log_file for details)"
  fi
  return $status
}

# Download models in parallel with a maximum number of parallel jobs
pids=()
success_count=0
fail_count=0

for pair in "${LANGUAGE_PAIRS[@]}"; do
  # If we've reached max parallel, wait for one to finish
  while [ ${#pids[@]} -ge $MAX_PARALLEL ]; do
    for i in "${!pids[@]}"; do
      if ! kill -0 ${pids[$i]} 2>/dev/null; then
        wait ${pids[$i]}
        status=$?
        if [ $status -eq 0 ]; then
          ((success_count++))
        else
          ((fail_count++))
        fi
        unset pids[$i]
        break
      fi
    done
    # Reindex array to remove gaps
    pids=("${pids[@]}")
    sleep 0.5
  done
  
  # Start a new download
  download_pair "$pair" &
  pids+=($!)
  echo "Started download for $pair (PID: ${pids[-1]}, active: ${#pids[@]})"
done

# Wait for remaining processes to finish
for pid in "${pids[@]}"; do
  wait $pid
  status=$?
  if [ $status -eq 0 ]; then
    ((success_count++))
  else
    ((fail_count++))
  fi
done

echo "Argos Translate language models download completed!"
echo "Results: $success_count models installed successfully, $fail_count failed"

# Create a simple script to list installed packages
echo "#!/bin/bash
echo 'Listing installed packages with argospm:'
argospm list 2>/dev/null || echo 'argospm list command failed'

echo 'Listing installed packages with Python API:'
python3 -c '
import argostranslate.package
installed_packages = argostranslate.package.get_installed_packages()
print(f\"Found {len(installed_packages)} installed packages:\")
for pkg in installed_packages:
    print(f\"- {pkg.from_code} to {pkg.to_code}: {pkg.package_path}\")
'
" > "$MODELS_DIR/list_models.sh"
chmod +x "$MODELS_DIR/list_models.sh"

# List installed packages
echo "Installed Argos Translate packages:"
"$MODELS_DIR/list_models.sh"