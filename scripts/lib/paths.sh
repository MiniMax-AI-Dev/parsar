#!/usr/bin/env bash
# Centralised default path layout for any local-dev script.
#
# Source this from scripts that need to write runtime artefacts under
# ~/.parsar/. Keeping the layout here stops the dev tooling from
# silently scattering log / state / cache directories across the repo
# checkout. The actual directory creation lives in setup.sh; this file
# just exposes the variables so non-install code paths can reference
# them without re-creating them.
#
# PARSAR_HOME can be overridden by callers before sourcing; the rest
# derive from it.
PARSAR_HOME="${PARSAR_HOME:-$HOME/.parsar}"
PARSAR_CONFIG_DIR="$PARSAR_HOME/config"
PARSAR_LOG_DIR="$PARSAR_HOME/logs"
PARSAR_STATE_DIR="$PARSAR_HOME/state"
PARSAR_CACHE_DIR="$PARSAR_HOME/cache"
PARSAR_DEV_DIR="$PARSAR_HOME/dev"
export PARSAR_HOME PARSAR_CONFIG_DIR PARSAR_LOG_DIR PARSAR_STATE_DIR PARSAR_CACHE_DIR PARSAR_DEV_DIR
