# AionFS Dev Server (MVP Surface)

The `aionfs-devd` binary provides a lightweight HTTP façade that Piccolod can integrate with while the full federation engine is still under construction. It focuses on volume lifecycle calls and persists state to a JSON file so reloads keep existing records.

## Running
```bash
# From the repo root
mkdir -p data
go run ./cmd/aionfs-devd \
  -listen 127.0.0.1:7081 \
  -data-dir ./data \
  -tls-cert ./certs/dev-server.pem \
  -tls-key ./certs/dev-server-key.pem \
  -tls-client-ca ./certs/dev-clients-ca.pem
```

- `-listen`: address for the listener (default `0.0.0.0:7080`).
- `-data-dir`: directory where `state.json` will be created for persistent dev state.
- `-tls-cert` / `-tls-key`: enable TLS when both are provided.
- `-tls-client-ca`: optional bundle to enforce mutual TLS (clients must present certs signed by this CA).
- `-token-file`: JSON map of `{ "token": "principal" }` entries. When provided, every `/v1` request must use a `Bearer <token>` header that maps to the calling principal.

Omit the TLS flags if you want a plain HTTP endpoint for local prototyping. A basic health check is available at `GET /healthz`.

### Generating Local Certificates

For quick experiments you can mint a throwaway CA and issue a client/server pair:

```bash
mkdir -p certs
openssl req -x509 -newkey rsa:2048 -days 30 -nodes \
  -keyout certs/dev-ca-key.pem -out certs/dev-ca.pem \
  -subj "/CN=AionFS Dev CA"
openssl req -new -nodes -newkey rsa:2048 \
  -keyout certs/dev-server-key.pem -out certs/dev-server.csr \
  -subj "/CN=127.0.0.1"
openssl x509 -req -in certs/dev-server.csr -CA certs/dev-ca.pem \
  -CAkey certs/dev-ca-key.pem -CAcreateserial -days 30 \
  -out certs/dev-server.pem
cp certs/dev-ca.pem certs/dev-clients-ca.pem # trust the same CA for client certs
```

Create client certificates the same way and configure your HTTP client (or Piccolod) to present them during TLS handshakes.

## HTTP Endpoints
All responses are JSON. The canonical interface is HTTPS/mTLS, but the dev server exports plain HTTP for rapid iteration.

### Create a Volume
```http
POST /v1/volumes
Content-Type: application/json

{
  "owner_principal": "service:app1",
  "class": "persistent",
  "quota_bytes": 21474836480,
  "policy_profile": "standard",
  "export_mode": "fs"
}
```

Response includes a generated `volume_id`, a fake host path (`/run/aionfs/mounts/<id>`), and timestamps.

### List / Inspect Volumes
- `GET /v1/volumes`
- `GET /v1/volumes/{volume_id}`

### Attach / Detach
```http
POST /v1/volumes/{volume_id}/attach
Content-Type: application/json

{
  "principal": "service:app1",
  "session_id": "sess-local",
  "consumer_endpoint": "podman://piccolod/app1"
}
```

- Principal must match the owner recorded at creation time.
- Session IDs are optional; when omitted the server generates one.
- `POST /v1/volumes/{volume_id}/detach` clears the active session and returns the volume metadata.

### Delete
`DELETE /v1/volumes/{volume_id}` removes the record from the JSON store.

## Data Persistence
State is stored at `<data-dir>/state.json`. The server uses coarse locking and rewrites the file on every change—sufficient for development but not intended for production scale.

## Next Steps
- Replace the JSON store with the real metadata service once federation primitives land.
- Layer TLS/mTLS and token auth in front of the dev API so Piccolod integration can reuse the production security model.
- Expand the surface with snapshot and checkpoint mocks as Piccolod’s requirements firm up.

## Token File Example

```json
{
  "secrettoken123": "service:demo",
  "admin-token": "admin:ops"
}
```

Clients send `Authorization: Bearer secrettoken123` with each request; the server enforces that any declared `owner_principal` matches the token principal.

## Snapshots & Checkpoints

- `POST /v1/volumes/{volume_id}/snapshots` captures a stub snapshot record (returns `snapshot_id`).
- `GET /v1/volumes/{volume_id}/snapshots` lists stored snapshots for the volume.
- `POST /v1/checkpoints` creates a checkpoint manifest linking the latest snapshot per requested volume (or every volume owned by the caller when `volume_ids` is omitted).
- `GET /v1/checkpoints` lists checkpoint manifests visible to the caller.

These endpoints are metadata-only today; no actual data copy occurs, but they unblock Piccolod integration flows.
