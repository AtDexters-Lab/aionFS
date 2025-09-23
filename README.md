# AionFS (Dev Surface)

AionFS is the storage service that underpins Piccolo's federated persistence layer. This repository currently exposes a development façade so other teams (e.g., Piccolod) can integrate against the HTTP surface while the full replication engine is under construction.

## Quick Start (Native)

```bash
# Install dependencies: Go 1.23+, openssl, podman (optional)
make build
make run  # listens on 127.0.0.1:7081 with JSON state in ./data
```

The dev server persists metadata (volumes, snapshots, checkpoints) to `state.json` inside the configured `-data-dir`.

## Quick Start (Container / Podman)

```bash
make gen-token            # writes tokens.json with a demo token
make gen-dev-certs        # creates certs/ for TLS experiments
make run-container        # podman run -p 7080:7080 ...
```

Environment-specific overrides:

- `CONTAINER_TOOL` (default `podman`, switch to `docker` if needed)
- `IMAGE`, `DATA_DIR`, `TOKEN_FILE`, `CERT_DIR`

The container entrypoint is `aionfs-devd`; use `podman logs` to inspect output.

## API Surface

All endpoints live under `/v1` and speak JSON:

- `POST /v1/volumes` – create a volume
- `POST /v1/volumes/{id}/attach|detach`
- `POST /v1/volumes/{id}/snapshots` – record a snapshot stub
- `POST /v1/checkpoints` – assemble a checkpoint from latest snapshots
- `GET /v1/volumes|.../snapshots|/checkpoints`

Authentication: provide `Authorization: Bearer <token>` headers when the server is launched with `-token-file`. TLS/mTLS parameters mirror production (see `docs/dev-server.md`).

## Tooling

- `scripts/gen-dev-certs.sh` – generates a local CA and server cert/key pair
- `scripts/gen-token.sh` – emits a JSON token map for bearer auth
- `Makefile` – common build, test, podman, and cleanup targets

## Roadmap

- Replace JSON persistence with the real metadata engine
- Implement erasure coding and shard placement
- Expand API coverage (maintenance, federation membership, metrics)

Contributions welcome! Check `AGENTS.md` for coding guidelines.

## Releases

Tagged releases trigger the GitHub Actions *Container* workflow, which builds a multi-arch image and publishes it to GHCR. To cut a release:

```bash
git tag v0.1.0
git push origin v0.1.0
# or use gh release create v0.1.0 --generate-notes
```

Artifacts:
- Image: `ghcr.io/<org>/aionfs-devd:<tag>` and `:latest`
- Cosign signature: `cosign verify ghcr.io/<org>/aionfs-devd:<tag>`

Piccolod should reference the image by digest in production manifests; run `podman pull ghcr.io/<org>/aionfs-devd:<tag>` to fetch it locally.
