
# Define the files to search
$files = @(
    "data.db.part0",
    "data.db.part1",
    "data.db.part3",
    "data.db.part4"
)

# Function to determine the VLC command
function Determine-VLC {
    if (Get-Command "vlc" -ErrorAction SilentlyContinue) {
        return "vlc"
    } elseif (Get-Command "cvlc" -ErrorAction SilentlyContinue) {
        return "cvlc"
    } elseif (Test-Path "C:\Program Files\VideoLAN\VLC\vlc.exe") {
        return "C:\Program Files\VideoLAN\VLC\vlc.exe"
    } else {
        Write-Error "No VLC player found. Please install VLC."
        exit 1
    }
}

# Get the VLC command
$vlcCommand = Determine-VLC

# Combine and process the files
$content = foreach ($file in $files) {
    if (Test-Path $file) {
        Get-Content $file
    }
}

# Use fzf for searching (requires fzf installed and accessible from PowerShell)
$fzfPath = "fzf.exe"  # Adjust path to fzf.exe if necessary
if (-not (Get-Command $fzfPath -ErrorAction SilentlyContinue)) {
    Write-Error "fzf not found. Please install fzf and ensure it's in your PATH."
    exit 1
}

# Pass the content through fzf
$selected = $content |
    ForEach-Object { $_ -split '###' } |
    ForEach-Object { "$($_[1])`t$($_ -join '###')" } |
    & $fzfPath --with-nth=1 --delimiter="`t" `
        --preview="pwsh -c `"`
            Write-Output 'Selected Entry:'; `
            Write-Output 'ID: $($_.Split('###')[0])'; `
            Write-Output 'Name: $($_.Split('###')[1])'; `
            Write-Output 'Path: $($_.Split('###')[2])'; `
            Write-Output 'Is File: $($_.Split('###')[3])'; `
            Write-Output 'Parent ID: $($_.Split('###')[4])'`""

# If no selection was made, exit
if (-not $selected) {
    Write-Output "No selection made."
    exit 0
}

# Extract the file path from the selected entry
$filePath = ($selected -split '###')[2]

# Convert Linux-style paths to Windows-style paths for VLC.exe
if ($vlcCommand -match "vlc.exe") {
    $filePath = wsl.exe wslpath -w $filePath
}

# Stream the file using VLC
Write-Output "Streaming file: $filePath"
& $vlcCommand $filePath
