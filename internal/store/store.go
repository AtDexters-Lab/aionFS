package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Volume represents the minimal metadata tracked by the dev server.
type Volume struct {
	VolumeID       string    `json:"volume_id"`
	OwnerPrincipal string    `json:"owner_principal"`
	Class          string    `json:"class"`
	QuotaBytes     int64     `json:"quota_bytes"`
	PolicyProfile  string    `json:"policy_profile"`
	ExportMode     string    `json:"export_mode"`
	MountHandle    MountInfo `json:"mount_handle"`
	AttachState    string    `json:"attach_state"`
	AttachSession  *Session  `json:"attach_session,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// MountInfo exposes information about the prepared export.
type MountInfo struct {
	Mode     string `json:"mode"`
	HostPath string `json:"host_path"`
	State    string `json:"state"`
}

// Session captures attach metadata for bookkeeping.
type Session struct {
	SessionID        string    `json:"session_id"`
	Principal        string    `json:"principal"`
	ConsumerEndpoint string    `json:"consumer_endpoint"`
	AttachedAt       time.Time `json:"attached_at"`
}

// Snapshot represents a point-in-time capture placeholder.
type Snapshot struct {
	SnapshotID string    `json:"snapshot_id"`
	VolumeID   string    `json:"volume_id"`
	CreatedAt  time.Time `json:"created_at"`
	Note       string    `json:"note,omitempty"`
}

// Checkpoint groups snapshot identifiers for recovery stubs.
type Checkpoint struct {
	ManifestID  string    `json:"manifest_id"`
	SnapshotIDs []string  `json:"snapshot_ids"`
	CreatedAt   time.Time `json:"created_at"`
	Note        string    `json:"note,omitempty"`
}

type fileState struct {
	Volumes     map[string]Volume     `json:"volumes"`
	Snapshots   map[string][]Snapshot `json:"snapshots"`
	Checkpoints map[string]Checkpoint `json:"checkpoints"`
}

// FileStore is a naive JSON-backed persistence layer for dev use.
type FileStore struct {
	mu      sync.RWMutex
	path    string
	volumes map[string]Volume
	snaps   map[string][]Snapshot
	cp      map[string]Checkpoint
}

// ErrVolumeNotFound is returned when a requested ID does not exist.
var ErrVolumeNotFound = errors.New("volume not found")

// NewFileStore loads persisted state (if present) from disk.
func NewFileStore(dataDir string) (*FileStore, error) {
	st := &FileStore{
		path:    filepath.Join(dataDir, "state.json"),
		volumes: map[string]Volume{},
		snaps:   map[string][]Snapshot{},
		cp:      map[string]Checkpoint{},
	}

	if err := st.load(); err != nil {
		return nil, err
	}
	return st, nil
}

// Close is present for symmetry; it currently just flushes to disk.
func (s *FileStore) Close() error {
	return s.flush()
}

// ListVolumes returns all known volumes.
func (s *FileStore) ListVolumes() []Volume {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Volume, 0, len(s.volumes))
	for _, v := range s.volumes {
		out = append(out, v)
	}
	return out
}

// ListVolumesByOwner returns volumes filtered by owner principal.
func (s *FileStore) ListVolumesByOwner(owner string) []Volume {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Volume, 0)
	for _, v := range s.volumes {
		if v.OwnerPrincipal == owner {
			out = append(out, v)
		}
	}
	return out
}

// GetVolume returns volume metadata by ID.
func (s *FileStore) GetVolume(id string) (Volume, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.volumes[id]
	if !ok {
		return Volume{}, ErrVolumeNotFound
	}
	return v, nil
}

// PutVolume upserts volume metadata and persists to disk.
func (s *FileStore) PutVolume(v Volume) (Volume, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	v.UpdatedAt = now
	if existing, ok := s.volumes[v.VolumeID]; ok {
		if !existing.CreatedAt.IsZero() {
			v.CreatedAt = existing.CreatedAt
		} else {
			v.CreatedAt = now
		}
	} else {
		v.CreatedAt = now
	}
	s.volumes[v.VolumeID] = v
	if err := s.flushLocked(); err != nil {
		return Volume{}, err
	}
	return v, nil
}

// AddSnapshot stores a snapshot record for a volume.
func (s *FileStore) AddSnapshot(volumeID string, snap Snapshot) (Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.volumes[volumeID]; !ok {
		return Snapshot{}, ErrVolumeNotFound
	}
	list := append(s.snaps[volumeID], snap)
	s.snaps[volumeID] = list
	if err := s.flushLocked(); err != nil {
		return Snapshot{}, err
	}
	return snap, nil
}

// ListSnapshots returns snapshot records for a volume.
func (s *FileStore) ListSnapshots(volumeID string) []Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]Snapshot{}, s.snaps[volumeID]...)
}

// LatestSnapshot returns the most recent snapshot for a volume.
func (s *FileStore) LatestSnapshot(volumeID string) (Snapshot, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	snaps := s.snaps[volumeID]
	if len(snaps) == 0 {
		return Snapshot{}, false
	}
	return snaps[len(snaps)-1], true
}

// VolumeIDForSnapshot finds the owning volume for a snapshot id.
func (s *FileStore) VolumeIDForSnapshot(snapshotID string) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for vid, snaps := range s.snaps {
		for _, snap := range snaps {
			if snap.SnapshotID == snapshotID {
				return vid, true
			}
		}
	}
	return "", false
}

// PutCheckpoint stores a checkpoint manifest.
func (s *FileStore) PutCheckpoint(cp Checkpoint) (Checkpoint, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cp[cp.ManifestID] = cp
	if err := s.flushLocked(); err != nil {
		return Checkpoint{}, err
	}
	return cp, nil
}

// ListCheckpoints returns all checkpoint manifests.
func (s *FileStore) ListCheckpoints() []Checkpoint {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Checkpoint, 0, len(s.cp))
	for _, v := range s.cp {
		out = append(out, v)
	}
	return out
}

// DeleteVolume removes a volume.
func (s *FileStore) DeleteVolume(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.volumes[id]; !ok {
		return ErrVolumeNotFound
	}
	delete(s.volumes, id)
	return s.flushLocked()
}

func (s *FileStore) load() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := os.Open(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("open state file: %w", err)
	}
	defer f.Close()

	var fs fileState
	if err := json.NewDecoder(f).Decode(&fs); err != nil {
		return fmt.Errorf("decode state: %w", err)
	}
	if fs.Volumes == nil {
		fs.Volumes = map[string]Volume{}
	}
	if fs.Snapshots == nil {
		fs.Snapshots = map[string][]Snapshot{}
	}
	if fs.Checkpoints == nil {
		fs.Checkpoints = map[string]Checkpoint{}
	}
	s.volumes = fs.Volumes
	s.snaps = fs.Snapshots
	s.cp = fs.Checkpoints
	return nil
}

func (s *FileStore) flush() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.flushLocked()
}

func (s *FileStore) flushLocked() error {
	tmpPath := s.path + ".tmp"
	payload := fileState{
		Volumes:     s.volumes,
		Snapshots:   s.snaps,
		Checkpoints: s.cp,
	}
	f, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("open temp state: %w", err)
	}
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	if err := enc.Encode(&payload); err != nil {
		f.Close()
		return fmt.Errorf("encode state: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close temp state: %w", err)
	}
	if err := os.Rename(tmpPath, s.path); err != nil {
		return fmt.Errorf("replace state file: %w", err)
	}
	return nil
}
