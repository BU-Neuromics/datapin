# datapin installer — Windows (PowerShell)
#
# Usage (run in PowerShell):
#   irm https://raw.githubusercontent.com/BU-Neuromics/datapin/main/install.ps1 | iex
#
# Override install directory:
#   $env:DATAPIN_INSTALL_DIR = "C:\tools"; irm ... | iex

$ErrorActionPreference = 'Stop'

$Repo   = "BU-Neuromics/datapin"
$Binary = "datapin"

function Info { Write-Host "==> $args" -ForegroundColor Blue }
function Ok   { Write-Host "  v $args" -ForegroundColor Green }
function Bail { Write-Host "Error: $args" -ForegroundColor Red -ErrorAction Stop; throw $args }

# ---- detect architecture ----

$Arch = switch ($env:PROCESSOR_ARCHITECTURE) {
  "AMD64" { "amd64" }
  "ARM64" { "arm64" }
  default { Bail "Unsupported architecture: $($env:PROCESSOR_ARCHITECTURE)" }
}

# ---- choose install directory ----

$InstallDir = if ($env:DATAPIN_INSTALL_DIR) {
  $env:DATAPIN_INSTALL_DIR
} else {
  Join-Path $env:LOCALAPPDATA "Programs\datapin"
}
New-Item -ItemType Directory -Force -Path $InstallDir | Out-Null

# ---- fetch latest version from GitHub API ----

Info "Fetching latest release..."
$Release = Invoke-RestMethod "https://api.github.com/repos/$Repo/releases/latest"
$Version = $Release.tag_name
$Ver     = $Version.TrimStart('v')

# ---- download archive + checksum file ----

$Archive  = "${Binary}_${Ver}_windows_${Arch}.zip"
$BaseUrl  = "https://github.com/$Repo/releases/download/$Version"
$Tmp      = Join-Path $env:TEMP "datapin-install-$(Get-Random)"
New-Item -ItemType Directory -Force -Path $Tmp | Out-Null

try {
  Info "Downloading $Binary $Version (windows/$Arch)..."
  Invoke-WebRequest "$BaseUrl/$Archive"      -OutFile "$Tmp\$Archive"
  Invoke-WebRequest "$BaseUrl\checksums.txt" -OutFile "$Tmp\checksums.txt"

  # ---- verify SHA-256 checksum ----

  Info "Verifying checksum..."
  $Expected = (Get-Content "$Tmp\checksums.txt" |
    Where-Object { $_ -match [regex]::Escape($Archive) }) -split '\s+' |
    Select-Object -First 1
  if (-not $Expected) { Bail "No checksum entry found for $Archive." }
  $Actual = (Get-FileHash "$Tmp\$Archive" -Algorithm SHA256).Hash.ToLower()
  if ($Actual -ne $Expected) {
    Bail "Checksum mismatch.`nExpected: $Expected`nGot:      $Actual"
  }
  Ok "Checksum verified."

  # ---- extract and install ----

  Info "Installing to $InstallDir..."
  Expand-Archive "$Tmp\$Archive" -DestinationPath $Tmp -Force
  $Exe = Get-ChildItem -Path $Tmp -Filter "$Binary.exe" -Recurse | Select-Object -First 1
  if (-not $Exe) { Bail "$Binary.exe not found in archive." }
  Copy-Item $Exe.FullName "$InstallDir\$Binary.exe" -Force

  Ok "Installed: $(& "$InstallDir\$Binary.exe" --version)"

  # ---- add to user PATH if needed ----

  $UserPath = [Environment]::GetEnvironmentVariable("Path", "User")
  if ($UserPath -notlike "*$InstallDir*") {
    [Environment]::SetEnvironmentVariable("Path", "$UserPath;$InstallDir", "User")
    Write-Host "`nNote: Added $InstallDir to your PATH." -ForegroundColor Yellow
    Write-Host "Restart your terminal for the change to take effect."
  }
}
finally {
  Remove-Item $Tmp -Recurse -Force -ErrorAction SilentlyContinue
}
