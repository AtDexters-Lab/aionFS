#!/usr/bin/env bash
set -euo pipefail

CERT_DIR=${1:-certs}
mkdir -p "$CERT_DIR"

CA_KEY="$CERT_DIR/ca-key.pem"
CA_CERT="$CERT_DIR/ca.pem"
SERVER_KEY="$CERT_DIR/server-key.pem"
SERVER_CSR="$CERT_DIR/server.csr"
SERVER_CERT="$CERT_DIR/server.pem"
CLIENT_CA="$CERT_DIR/client-ca.pem"

if [[ -f $CA_KEY ]]; then
  echo "CA already exists in $CERT_DIR; aborting to avoid overwrite" >&2
  exit 1
fi

openssl req -x509 -newkey rsa:2048 -days 365 -nodes \
  -keyout "$CA_KEY" -out "$CA_CERT" \
  -subj "/CN=AionFS Dev CA" >/dev/null

openssl req -new -nodes -newkey rsa:2048 \
  -keyout "$SERVER_KEY" -out "$SERVER_CSR" \
  -subj "/CN=localhost" >/dev/null

openssl x509 -req -in "$SERVER_CSR" -CA "$CA_CERT" -CAkey "$CA_KEY" \
  -CAcreateserial -days 120 -out "$SERVER_CERT" >/dev/null

cp "$CA_CERT" "$CLIENT_CA"
rm -f "$SERVER_CSR"

cat <<INFO
Generated certificates in $CERT_DIR:
- CA: ca.pem (private key ca-key.pem)
- Server certificate: server.pem (key server-key.pem)
- Client trust bundle: client-ca.pem
INFO
