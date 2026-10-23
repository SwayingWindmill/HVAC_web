# The delivery file: 30 fps at delivery quality, with the score (film/assets/audio/score.wav) mixed in.
# Fast page moves carry their own blur in the composition; frame blending is not used, because two
# samples of a fast-moving page read as a double image rather than as blur.
# Usage: scripts\deliver.ps1 [output name]
param([string]$Name = 'film')
$ErrorActionPreference = 'Stop'
& (Join-Path $PSScriptRoot 'hf-win.ps1') render --browser-gpu --quality delivery --output "renders\$Name.mp4"
exit $LASTEXITCODE
