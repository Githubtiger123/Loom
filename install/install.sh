#!/usr/bin/env bash
# Linux / WSL / Git Bash entry point. Never installs workstation dependencies.
set -euo pipefail
INSTALL_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
umask 077
exec "${LOOM_INSTALL_PYTHON:-python3}" "$INSTALL_DIR/scripts/install.py" "$@"
