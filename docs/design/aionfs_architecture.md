# AionFS Architecture & API Blueprint

## Scope & Objectives
- Deliver a self-contained, container-friendly storage service that exposes a unified persistence interface for Piccolo and non-Piccolo orchestrators.
- Operate autonomously: join and serve the federation at boot, even if higher-layer orchestrators (e.g., piccolod) are locked or offline.
- Support adaptive, erasure-coded redundancy across heterogeneous deployments: single-node, self-managed multi-node meshes, and Piccolospace-hosted networks.
- Enforce strong isolation between tenants/services via identity-bound volume ownership and dual-layer encryption (user secret + TPM sealing when available).

## Deployment Topologies
- **Local solo**: one host, one AionFS node, multiple storage targets. Redundancy policy can degrade to `data=n, parity=0` for pure local disks.
- **Self-managed federation**: peer operators exchange trust bundles or invites to form a mesh. Each node exposes a public or relay-assisted endpoint, encapsulating traffic with mTLS.
- **Piccolospace-managed**: an external directory issues credentials, maintains peer metadata, and provides relay services when direct connectivity is unavailable.
- AionFS runs in a privileged container with optional sidecars for disk prep. Required mounts/devices: `/dev/tpmrm0` (when present), host block devices (read), and bind targets for exported volumes.

## Component Architecture
- **Control Plane**
  - `API Gateway`: HTTP/JSON server handling authentication, RBAC, rate limiting, and version negotiation.
  - `Config & State Store`: transactional DB (SQLite + WAL) for federations, nodes, volumes, shards, checkpoints, policies, and audit log.
  - `Policy Engine`: resolves effective policies (redundancy, encryption, retention, quotas) before dispatching operations.
- **Data Plane**
  - `Volume Orchestrator`: provisions filesystems or block exports, coordinates encryption, and mediates mount sessions.
  - `Snapshot & Checkpoint Engine`: captures consistent snapshots, generates manifests, and orchestrates replication/repair.
  - `Replication Transport`: QUIC-based worker that encodes/decodes erasure-coded shards, streams data peer-to-peer, and verifies integrity.
  - `Encryption Layer`: generates per-volume keys, wraps them with user secrets, and seals bundles with TPM when accessible.
- **Bootstrap & Identity**
  - `Credential Vault`: maintains node certificates, join tokens, and recovery secrets; leverages TPM sealing or passphrase-protected vaults.
  - `Discovery Agent`: synchronizes peer lists via static manifests or external directories, monitors certificate expiry, and refreshes endpoints.
- **Observability & Maintenance**
  - `Metrics Endpoint`: Prometheus/OpenTelemetry exporter (replication lag, shard divergence, disk pressure).
  - `Event Stream`: structured events over SSE/WebSocket (`volume.created`, `attach.denied`, `peer.unreachable`).
  - `Maintenance Jobs`: background disk health scans (SMART/NVMe), shard scrubbing, cache eviction, and rebalance tasks.

## Node State, Portability & Disk Identity
- Goals
  - Entire AionFS node state is portable: backup, move, and restore on any host without depending on host-specific device naming.
  - Disks managed by AionFS are re-identified by AionFS itself, not by OS paths or udev aliases, so `/dev/sdX` or `/dev/nvmeY` renumbering is harmless.
  - Secrets remain protected yet recoverable: default dual-wrapping with TPM sealing when available plus an operator recovery passphrase or external directory key.

- State Surface (single mount inside the container)
  - Default path: `/var/lib/aionfs` (bind a durable volume here).
  - Contents (illustrative):
    - `state.db` (SQLite + WAL): federations, nodes, volumes, shards, targets, policies, audit.
    - `vault/` (sealed secrets): node certs, join tokens, recovery keys; dual-wrapped `tpm.sealed` + `recovery.enc`.
    - `manifests/` and `checkpoints/`: local copies of checkpoint manifests and capsule metadata.
    - `targets/`: cache of storage target probes and fingerprints.
    - `logs/`, `events/` (optional): rolling archives for troubleshooting.
  - This directory is the only required backup for node state; data payload lives on disks as shards.

- Backups & Restore
  - Online export: `aionfsctl state export --out aionfs-state-v1.tar.zst` performs a WAL checkpoint, quiesces schema migrations, and snapshots `state.db`, `vault/`, and metadata subtrees.
  - Import: `aionfsctl state import --in aionfs-state-v1.tar.zst` restores files, rehydrates vault, and triggers a rescan of attached disks.
  - Cold copy is also valid when the daemon is stopped. For online backups taken by external tooling, use `POST /v1/state/snapshot` to freeze and `POST /v1/state/unfreeze` to resume.

- Disk/Target Identity (host-OS independent)
  - AionFS assigns a stable `target_id` per admitted storage target and writes an AionFS superblock label to the device/partition:
    - Superblock fields: `magic`, `version`, `target_uuid`, `created_at`, `last_seen`, `node_id_at_onboarding`, `hw_fingerprint_set`, and optional `luks_uuid`/`fs_uuid` anchors.
    - Location: first safe metadata region (end-of-disk guard by default, fallback to first MiB on empty devices). Redundant copy stored to a secondary offset.
  - Probe order when discovering devices after a move/restore:
    1) Scan block devices; for each, read AionFS superblock. If present and authentic, map directly to `target_id`.
    2) If no superblock, derive a hardware fingerprint using controller-level identifiers via direct ioctls (not udev):
       - NVMe: Controller/Namespace Identify → `serial`, `eui64`/`nguid`.
       - SATA/SAS/SCSI: INQUIRY VPD pages 0x80/0x83 → `unit serial` and `wwn`.
       - USB: device serial tuple when exposed by the bridge.
       Canonicalize and hash (BLAKE3) into `hw_fp`. Match against `hw_fingerprint_set` captured at onboarding.
    3) Validate by reading the expected `luks_uuid` or partition table GUIDs when encryption/partitioning is used.
  - Onboarding path (`POST /v1/storage-targets`):
    - Generate a random `target_uuid` (v4), capture the `hw_fingerprint_set`, write superblock(s), and persist mapping to `state.db`.
    - If encryption is enabled, label the container `LUKS2` name as `AIONFS:<target_uuid>` and record its `luks_uuid`.
  - Safety against clones: if two devices present the same superblock (e.g., byte-for-byte clones), AionFS flags `target.duplicate` and requires operator selection before use.

- Cross‑Machine Restore Example
  1) Operator attaches three disks to a new host and mounts the prior node’s state volume at `/var/lib/aionfs`.
  2) Start AionFS. The daemon restores node identity from `vault/`, joins the federation, and scans block devices.
  3) For each disk, the superblock reveals `target_uuid` → the daemon reconstructs the shard/placement maps from `state.db` and on‑disk anchors.
  4) Volumes transition to `degraded` or `healthy` based on shard quorum; replication/repair proceeds automatically per policy.

- Security
  - Dual-wrap by default: all critical keys are sealed to TPM when available and also wrapped with an operator recovery secret (or directory-managed key). Either unwrap path restores state.
  - State exports can be `--redact-secrets` to omit private keys and include only CSR material to re-issue certs from a directory.

- Operational Interfaces (preview)
  - `POST /v1/state/export` and `POST /v1/state/import` for automation-friendly backups and restores.
  - `POST /v1/state/snapshot` / `POST /v1/state/unfreeze` to coordinate external snapshot tools.
  - `POST /v1/storage-targets/probe` to dump current superblock and hardware fingerprints for diagnostics.

## Domain Model Snapshot
| Object | Key Fields | Notes |
| --- | --- | --- |
| Federation | `federation_id`, trust roots, erasure policy (`data_shards`, `parity_shards`, `min_peer_diversity`), retention defaults, cache profile | Single logical mesh; size-1 allowed |
| Node | `node_id`, certificates, capabilities (TPM, cache tier), health state, advertised endpoints, storage targets | One per physical host |
| StorageTarget | `target_id`, type (SATA/NVMe/USB/remote), capacity, health score, allocation pool | Must be explicitly admitted |
| CredentialBundle | Certificates, TPM binding metadata, recovery secret hints, issue/expiry timestamps | Sealed to TPM or passphrase |
| Volume | `volume_id`, owner principal, class (`persistent`/`ephemeral`), quota, policy override, encryption mode, placement manifest | Owner enforced during attach |
| Shard | `shard_index`, role (`data`/`parity`), location (node/target/path), checksum, generation | Only durable representation of payload |
| MountSession | `session_id`, `volume_id`, consumer principal, export mode (`fs`/`nbd`), host path/device, state | Ephemeral |
| Snapshot | `snapshot_id`, `volume_id`, creation time, shard versions, hashes, policy tags | Local per-volume capture |
| CheckpointManifest | `manifest_id`, set of snapshots + capsule metadata, erasure params, readiness state | Drives recovery |
| ReplicationJob | `job_id`, shard scope, source/targets, progress, retries, last error | Includes repairs/rebalance |
| CacheEntry | cache key, size, TTL, backing storage (RAM/SSD), last access | Optional acceleration tier |
| PolicyProfile | defaults for encryption, redundancy, retention, access ACLs | Referenced by volumes/services |

## Federation & Identity
- **Hybrid bootstrap**: nodes accept join tokens from either built-in controller (static trust bundle + invites) or external directory (Piccolospace). Both yield an mTLS certificate cached locally.
- **Public reachability**: prefer leveraging piccolod’s Nexus tunnel when present; otherwise use directory-provided relays or operator-supplied endpoints. Certificates are hot-reloaded when renewed.
- **Authorization**: mTLS identity + per-volume ACLs. Attach requests require a signed token binding to the owner principal; mismatches receive `403` and trigger an audit event.

## Storage Targets & Redundancy Policy
- Operators register targets via `POST /v1/storage-targets`. Unregistered disks stay untouched.
- Redundancy defined by erasure coding tuple `(data_shards, parity_shards)` plus `min_peer_diversity`. The policy engine scales shard counts based on federation size and health (e.g., 8+4 small mesh → 64+16 medium → 222+33 large).
- Placement considers node availability and storage-target health; repairs trigger when shard diversity drops below thresholds.
- Cache tier optionally stores hot objects as whole files on SSD/RAM while backing shards remain authoritative.

## Volume & Mount Lifecycle
1. **Provision**: orchestrator calls `POST /v1/volumes` with service principal, class, quota, desired policy overrides. Response includes `volume_id`, export mode, and initial mount handle.
2. **Prepare**: Volume Orchestrator allocates backing storage, formats FS (for `fs` mode), initializes encryption keys, seals secrets.
3. **Expose**: AionFS bind-mounts to `/run/aionfs/mounts/<volume_id>` or spawns `/dev/aionfs/<volume_id>` NBD device. Emits `volume.attached` event when ready.
4. **Attach**: Orchestrator instructs container runtime to mount the exported path/device. AionFS enforces owner principal; wrong principal → `403`.
5. **Detach**: `POST /v1/volumes/<id>:detach` quiesces export and tears down mount session. Volume remains provisioned for future re-attach.
6. **Delete/Archive**: `DELETE /v1/volumes/<id>` wipes shards, updates manifest, honors retention policies.

## Snapshot, Checkpoint & Recovery
- Snapshots triggered via `POST /v1/volumes/<id>/snapshots`; include hooks for FS freeze/thaw and application quiesce hints.
- Checkpoint manifests (`POST /v1/checkpoints`) bundle capsule metadata with consistent volume snapshots for orchestrated recoveries.
- Recovery flow: new node loads bootstrap bundle, authenticates to federation, pulls manifests, reconstructs shards to satisfy `data_shards` minimum, and replays capsule instructions.

## Cache Tier
- Configured per policy profile (`cache_mode = off | read-through | read-write`).
- Cache entries exposed via `GET /v1/cache/entries` (for observability) and `POST /v1/cache/purge` for targeted invalidation.
- Cache acceleration is transparent to clients; shard storage remains the authoritative source of truth.

## Operational Workflows
- **Disk onboarding**: discover via hardware scan → operator approves target → policy engine rebalances shards.
- **Health monitoring**: SMART/NVMe telemetry ingested into metrics/event stream; degraded targets trigger evacuation jobs.
- **Federation scaling**: directory or invite token issues new node credentials → node joins, advertises capacity → policy engine expands shard placement.
- **Node drain/decommission**: `POST /v1/nodes/<id>:drain` migrates shards away while maintaining redundancy thresholds.

## HTTP API Overview
All endpoints are served from the API Gateway over HTTPS with mTLS + bearer tokens (JWT or macaroons) for northbound clients. Piccolod-to-AionFS calls on the same host may also bind through an optional Unix domain socket exposed by the gateway, but the authenticated HTTPS surface remains canonical. Example base path: `/v1`.

### Authentication & Versioning
- mTLS client certs issued by federation authority (e.g., Piccolospace directory) identify nodes and trusted orchestrators.
- Bearer tokens scoped to principals (`service:piccolod`, `admin:ui`). Tokens are minted by the credential vault or external IdP.
- `X-AionFS-Version` header negotiates API schema revisions.

### Resource Endpoints
- `GET /v1/federation` → View federation defaults (trust roots, erasure policy, cache profile).
- `PATCH /v1/federation` → Update policy knobs (retention, redundancy ceilings) [admin only].
- `GET /v1/nodes` / `GET /v1/nodes/{id}` → Health, capabilities, endpoints.
- `POST /v1/nodes/{id}:drain` / `POST /v1/nodes/{id}:decommission` → Maintenance workflows.
- `GET /v1/storage-targets` / `POST /v1/storage-targets` / `PATCH /v1/storage-targets/{id}` / `DELETE ...` → Manage disks presented to AionFS.
- `POST /v1/volumes` / `GET /v1/volumes/{id}` / `GET /v1/volumes?owner=svc` → Provision and inspect volumes.
- `POST /v1/volumes/{id}:attach` / `POST /v1/volumes/{id}:detach` → Explicit attach/detach operations with signed principal tokens.
- `POST /v1/volumes/{id}:resize` / `POST /v1/volumes/{id}:change-policy` → Online adjustments.
- `POST /v1/volumes/{id}/snapshots` / `GET /v1/volumes/{id}/snapshots` → Snapshot management.
- `POST /v1/checkpoints` / `GET /v1/checkpoints/{id}` → Checkpoint manifests (capsule + volumes).
- `POST /v1/replication-jobs` / `GET /v1/replication-jobs/{id}` → Manual or scheduled repairs, migration tasks.
- `GET /v1/events/stream` → Server-sent events for orchestrators.
- `GET /v1/metrics` → Prometheus scrape endpoint.
- `GET /v1/cache/entries` / `POST /v1/cache/purge` → Cache observability and controls.
- `POST /v1/auth/tokens` → Mint scoped bearer tokens (requires mTLS client cert).

### Representative Payloads
```http
POST /v1/volumes
Content-Type: application/json
Authorization: Bearer <token>

{
  "owner_principal": "service:app1",
  "class": "persistent",
  "quota_bytes": 21474836480,
  "policy_profile": "standard",
  "export_mode": "fs"
}
```

```json
200 OK
{
  "volume_id": "vol-4d3c9b",
  "mount_handle": {
    "mode": "fs",
    "host_path": "/run/aionfs/mounts/vol-4d3c9b",
    "state": "preparing"
  },
  "encryption": {
    "mode": "dual",
    "tpm_sealed": true
  }
}
```

```http
POST /v1/volumes/vol-4d3c9b:attach
Content-Type: application/json
Authorization: Bearer <token>

{
  "session_id": "sess-2a17",
  "principal_token": "eyJhbGciOi...",
  "consumer_endpoint": "podman://piccolod/app1"
}
```

## External Federation Directory API (Preview)
- Base path: `/directory/v1`. All requests use mTLS with node or operator certificates issued by the directory CA.
- `POST /directory/v1/nodes/register`: node presents a join token, receives signed certificate bundle, relay endpoint list, and policy snapshot.
- `POST /directory/v1/nodes/renew`: rotate short-lived certificates before expiry.
- `GET /directory/v1/federations/{id}/peers`: retrieves peer roster (node ids, public endpoints, capabilities, health hints). Supports ETag for incremental sync.
- `POST /directory/v1/federations/{id}/tokens`: operator issues invite tokens with scope (`join_once`, `lifetime`), shard policy caps, and expiry.
- `POST /directory/v1/events/ingest`: nodes push health/telemetry (availability, capacity, shard deficits) so the directory can broker repairs/relay decisions.
- Directory publishes Webhook/SSE notifications (`peer.online`, `policy.updated`, `token.revoked`) for orchestrators to react in near real-time.

## Event & Error Semantics
- Errors follow RFC 7807 (`application/problem+json`). Important codes: `403` for principal mismatch, `409` for policy conflicts, `423` for volumes locked by ongoing snapshot, `503` when redundancy thresholds prevent attach.
- Events include correlation IDs tying attach attempts, snapshot runs, and replication jobs for end-to-end observability.

## Open Design Questions
- Implement the erasure coding engine using https://github.com/klauspost/reedsolomon (Go-optimized Reed-Solomon) and define the streaming chunk format.
- Define precise cache eviction strategy (LRU vs. LFU) and cache coherency for multi-producer workloads.
- Specify external directory API (for Piccolospace) to harmonize credential issuance and peer discovery.
