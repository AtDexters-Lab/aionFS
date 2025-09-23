#!/usr/bin/env bash
set -euo pipefail

TOKEN=${1:-$(openssl rand -hex 16)}
PRINCIPAL=${2:-service:demo}
OUTPUT=${3:-tokens.json}

cat <<JSON > "$OUTPUT"
{
  "$TOKEN": "$PRINCIPAL"
}
JSON

echo "Wrote token file to $OUTPUT"
echo "Token: $TOKEN"
echo "Principal: $PRINCIPAL"
