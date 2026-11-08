# Native PowerShell entry point; uses the same configuration and implementation.
$ErrorActionPreference = 'Stop'
$installerPython = if ($env:LOOM_INSTALL_PYTHON) { $env:LOOM_INSTALL_PYTHON } else { 'python' }
& $installerPython (Join-Path $PSScriptRoot 'scripts/install.py') @args
exit $LASTEXITCODE
