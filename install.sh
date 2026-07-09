#!/bin/bash
# Command line install alternative to the SPR UI plugin installer
echo "Please enter your SPR path (/home/spr/super/)"
read -r SUPERDIR

if [ -z "$SUPERDIR" ]; then
    SUPERDIR="/home/spr/super/"
fi

export SUPERDIR

echo "Please enter your SPR API token:"
read -r SPR_API_TOKEN

if [ -z "$SPR_API_TOKEN" ]; then
  echo "need api token, generate one on the auth keys page"
  exit 1
fi

mkdir -p "$SUPERDIR/configs/plugins/spr-reticulum"

# matches InstallTokenPath in plugin.json (what the UI installer writes)
printf '%s' "$SPR_API_TOKEN" > "$SUPERDIR/configs/plugins/spr-reticulum/api-token"
chmod 600 "$SUPERDIR/configs/plugins/spr-reticulum/api-token"

# default plugin config: AutoInterface on, transport off, TCP server off.
# Managed via the UI / PUT /config afterwards.
if [ ! -f "$SUPERDIR/configs/plugins/spr-reticulum/config.json" ]; then
cat > "$SUPERDIR/configs/plugins/spr-reticulum/config.json" <<'EOF'
{
  "EnableTransport": false,
  "AutoInterfaceEnabled": true,
  "TCPClientInterfaces": [],
  "TCPServerInterface": {
    "Enabled": false,
    "ListenPort": 4242
  },
  "LogLevel": 4
}
EOF
chmod 600 "$SUPERDIR/configs/plugins/spr-reticulum/config.json"
fi

./build_docker_compose.sh
docker compose up -d

CONTAINER_IP=$(docker inspect --format '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' "spr-reticulum")
API=127.0.0.1

# register the container on the spr-reticulum custom interface so it gets
# wan+dns access (matches NetworkCapabilities in plugin.json)
curl "http://${API}/firewall/custom_interface" \
-H "Authorization: Bearer ${SPR_API_TOKEN}" \
-X 'PUT' \
--data-raw "{\"SrcIP\":\"${CONTAINER_IP}\",\"Interface\":\"spr-reticulum\",\"Policies\":[\"wan\",\"dns\"],\"Groups\":[\"reticulum\"]}"

docker compose restart
