#!/bin/bash
# search_and_stream.sh

files="data.db.part0 data.db.part1 data.db.part2 data.db.part3"

# Define a function to check for VLC versions
determine_vlc() {
  if command -v vlc >/dev/null 2>&1; then
    echo "vlc"
  elif command -v cvlc >/dev/null 2>&1; then
    echo "cvlc"
  elif grep -qEi "(Microsoft|WSL)" /proc/version &>/dev/null; then
    echo "vlc.exe"
  else
    echo "No VLC player found. Please install VLC." >&2
    exit 1
  fi
}

# Get the VLC command
vlc_command=$(determine_vlc)

# Use fzf to search and select an entry
selected=$(cat $files | awk -F '###' '{print $2 "\t" $0}' |
  fzf --with-nth=1 --delimiter="\t" \
    --preview='
      echo "Selected Entry:"
      echo "ID: $(echo {} | awk -F "###" "{print \$1}")"
      echo "Name: $(echo {} | awk -F "###" "{print \$2}")"
      echo "Path: $(echo {} | awk -F "###" "{print \$3}")"
      echo "Is File: $(echo {} | awk -F "###" "{print \$4}")"
      echo "Parent ID: $(echo {} | awk -F "###" "{print \$5}")"
    ')

# If no selection was made, exit
if [ -z "$selected" ]; then
  echo "No selection made."
  exit 0
fi

# Extract the file path from the selected entry
file_path=$(echo "$selected" | awk -F '###' '{print $3}')

# Stream the file using VLC
echo "Streaming file: $file_path"
$vlc_command "$file_path"
