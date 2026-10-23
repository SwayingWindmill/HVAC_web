# Runs the Windows-side HyperFrames on the film, with the installed Chrome on the host GPU: the twin's
# WebGL renders on the graphics card here, while WSL only has software rendering.
# Usage: scripts\hf-win.ps1 render --browser-gpu --quality draft --output renders\draft.mp4
$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent $PSScriptRoot
$win = Join-Path $root 'win'
$env:HYPERFRAMES_NO_TELEMETRY = '1'
$env:DO_NOT_TRACK = '1'
$env:HYPERFRAMES_FFMPEG_PATH = Join-Path $win 'node_modules\ffmpeg-static\ffmpeg.exe'
$env:HYPERFRAMES_FFPROBE_PATH = Join-Path $win 'node_modules\ffprobe-static\bin\win32\x64\ffprobe.exe'
if (-not $env:HYPERFRAMES_BROWSER_PATH) { $env:HYPERFRAMES_BROWSER_PATH = 'C:\Program Files\Google\Chrome\Application\chrome.exe' }
Push-Location (Join-Path $root 'film')
try { & node (Join-Path $win 'node_modules\hyperframes\dist\cli.js') @args } finally { Pop-Location }
exit $LASTEXITCODE
