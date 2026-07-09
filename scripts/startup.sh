#!/bin/bash
set -a
. /configs/base/config.sh
if [ -f /configs/spr-reticulum/config.sh ]; then
    . /configs/spr-reticulum/config.sh
fi
set +a

# rnsd runs from a config dir in the state volume: Reticulum keeps its
# identity/storage under <configdir>/storage, and identities are secrets that
# belong in state, not configs. The plugin binary renders the RNS config into
# this directory (and a reference copy to /configs/spr-reticulum/rns.config)
# and supervises rnsd itself.
mkdir -p /state/plugins/spr-reticulum/rns
chmod 700 /state/plugins/spr-reticulum/rns

exec /reticulum_plugin
