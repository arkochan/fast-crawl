#!/bin/bash
# search_and_stream.sh

files="data.db*"

# Define a function to check for VLC versions
determine_vlc() {
  if command -v vlc >/dev/null 2>&1; then
    echo "vlc"
  elif command -v cvlc >/dev/null 2>&1; then
    echo "cvlc"
  elif grep -qEi "(Microsoft|WSL)" /proc/version &>/dev/null; then
    echo "vlc.exe"
  elif android=$(uname -a | grep -i android); then
    echo "am start -R 2 -S -n org.videolan.vlc/org.videolan.vlc.gui.video.VideoPlayerActivity -a android.intent.action.VIEW -d"
  else
    echo "No VLC player found. Please install VLC." >&2
    exit 1
  fi
}

# Get the VLC command
vlc_command=$(determine_vlc)

local preview='
      echo "Selected Entry:"
      echo "ID: $(echo {} | awk -F "###" "{print \$1}")"
      echo "Name: $(echo {} | awk -F "###" "{print \$2}")"
      echo "Path: $(echo {} | awk -F "###" "{print \$3}")"
      echo "Is File: $(echo {} | awk -F "###" "{print \$4}")"
      echo "Parent ID: $(echo {} | awk -F "###" "{print \$5}")"
    '
# Function to play a file
play_file() {
  local file_path=$1
  echo "Streaming file: $file_path"
  $vlc_command "$file_path"
  echo "$file_path" >.history
}

# Function to get siblings and sort them
get_siblings() {
  local parent_id=$1
  cat $files | awk -F '###' -v pid="$parent_id" '$5 == pid {print $0}' | sort -Vf >.history_siblings
}

# Handle arguments
if [ "$1" == "-c" ]; then
  if [ -f .history ]; then
    last_played=$(cat .history)
    play_file "$last_played"
  else
    echo "No history available."
    exit 1
  fi
  exit 0
elif [ "$1" == "-v" ]; then
  if [ -f .history_siblings -a -f .history ]; then
    last_played=$(cat .history)
    # next_file=$(awk -v last="$last_played" 'BEGIN {found=0} {if (found) {print; exit} if ($0 == last) found=1}' .history_siblings)
    next_file=$(
      cat .history_siblings | awk -F '###' '{print $2 "\t" $0}' |
        fzf --with-nth=1 --delimiter="\t" \
          --preview='
      echo "Selected Entry:"
      echo "ID: $(echo {} | awk -F "###" "{print \$1}")"
      echo "Name: $(echo {} | awk -F "###" "{print \$2}")"
      echo "Path: $(echo {} | awk -F "###" "{print \$3}")"
      echo "Is File: $(echo {} | awk -F "###" "{print \$4}")"
      echo "Parent ID: $(echo {} | awk -F "###" "{print \$5}")"
    '
    )
    if [ -n "$next_file" ]; then
      play_file "$next_file"
    else
      echo "No next file available."
      exit 1
    fi
  else
    echo "No history available."
    exit 1
  fi
elif [ "$1" == "-n" ]; then
  next_file=$(grep -A 1 "$(cat .history)" .history_siblings | tail -n 1 | awk -F '###' '{print $3}')
  play_file $next_file
  exit 0
fi

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

# Extract the file path and parent ID from the selected entry
file_path=$(echo "$selected" | awk -F '###' '{print $3}')
parent_id=$(echo "$selected" | awk -F '###' '{print $5}')

# Play the selected file
play_file "$file_path"

# Get and sort siblings
get_siblings "$parent_id"
