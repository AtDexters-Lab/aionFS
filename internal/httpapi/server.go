package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/AtDexters-Lab/aionFS/internal/auth"
	"github.com/AtDexters-Lab/aionFS/internal/store"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/google/uuid"
)

const (
	mountStatePreparing = "preparing"
	mountStateAttached  = "attached"
	mountStateAvailable = "available"
)

// Server exposes the dev HTTP interface.
type Server struct {
	store  *store.FileStore
	tokens auth.TokenProvider
}

// NewServer constructs a new HTTP server wrapper.
func NewServer(st *store.FileStore, tokens auth.TokenProvider) *Server {
	return &Server{store: st, tokens: tokens}
}

type principalKey struct{}

func (s *Server) requireAuth() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if s.tokens == nil {
				next.ServeHTTP(w, r)
				return
			}
			authz := r.Header.Get("Authorization")
			if !strings.HasPrefix(authz, "Bearer ") {
				respondError(w, http.StatusUnauthorized, "unauthorized", "missing bearer token")
				return
			}
			token := strings.TrimSpace(strings.TrimPrefix(authz, "Bearer "))
			principal, ok := s.tokens.Principal(token)
			if !ok {
				respondError(w, http.StatusUnauthorized, "unauthorized", "invalid token")
				return
			}
			ctx := context.WithValue(r.Context(), principalKey{}, principal)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func principalFromContext(ctx context.Context) (string, bool) {
	principal, ok := ctx.Value(principalKey{}).(string)
	return principal, ok
}

// Router builds the chi router with all routes mounted.
func (s *Server) Router() http.Handler {
	r := chi.NewRouter()
	r.Use(jsonMiddleware)
	r.Use(middleware.StripSlashes)

	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		respondJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	r.Route("/v1", func(r chi.Router) {
		if s.tokens != nil {
			r.Use(s.requireAuth())
		}
		r.Post("/volumes", s.handleCreateVolume)
		r.Get("/volumes", s.handleListVolumes)
		r.Post("/checkpoints", s.handleCreateCheckpoint)
		r.Get("/checkpoints", s.handleListCheckpoints)
		r.Route("/volumes/{volumeID}", func(r chi.Router) {
			r.Get("/", s.handleGetVolume)
			r.Post("/attach", s.handleAttachVolume)
			r.Post("/detach", s.handleDetachVolume)
			r.Post("/snapshots", s.handleCreateSnapshot)
			r.Get("/snapshots", s.handleListSnapshots)
			r.Delete("/", s.handleDeleteVolume)
		})
	})

	return r
}

type createVolumeRequest struct {
	OwnerPrincipal string `json:"owner_principal"`
	Class          string `json:"class"`
	QuotaBytes     int64  `json:"quota_bytes"`
	PolicyProfile  string `json:"policy_profile"`
	ExportMode     string `json:"export_mode"`
}

type errorResponse struct {
	Error   string `json:"error"`
	Message string `json:"message"`
}

func (s *Server) handleCreateVolume(w http.ResponseWriter, r *http.Request) {
	var req createVolumeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid_payload", "unable to decode request body")
		return
	}
	principal, havePrincipal := principalFromContext(r.Context())
	if s.tokens != nil {
		if !havePrincipal {
			respondError(w, http.StatusUnauthorized, "unauthorized", "token required")
			return
		}
	}

	if req.OwnerPrincipal == "" {
		if s.tokens != nil {
			req.OwnerPrincipal = principal
		} else {
			respondError(w, http.StatusBadRequest, "missing_owner", "owner_principal is required")
			return
		}
	} else if s.tokens != nil && req.OwnerPrincipal != principal {
		respondError(w, http.StatusForbidden, "principal_mismatch", "owner must match token principal")
		return
	}
	if req.ExportMode == "" {
		req.ExportMode = "fs"
	}
	if req.Class == "" {
		req.Class = "persistent"
	}

	volumeID := "vol-" + strings.ToLower(uuid.NewString()[:8])
	hostPath := path.Join("/run/aionfs/mounts", volumeID)

	v := store.Volume{
		VolumeID:       volumeID,
		OwnerPrincipal: req.OwnerPrincipal,
		Class:          req.Class,
		QuotaBytes:     req.QuotaBytes,
		PolicyProfile:  req.PolicyProfile,
		ExportMode:     req.ExportMode,
		MountHandle: store.MountInfo{
			Mode:     req.ExportMode,
			HostPath: hostPath,
			State:    mountStateAvailable,
		},
		AttachState: mountStateAvailable,
	}

	persisted, err := s.store.PutVolume(v)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "store_error", err.Error())
		return
	}

	respondJSON(w, http.StatusCreated, persisted)
}

func (s *Server) handleListVolumes(w http.ResponseWriter, r *http.Request) {
	principal, ok := principalFromContext(r.Context())
	if s.tokens != nil && !ok {
		respondError(w, http.StatusUnauthorized, "unauthorized", "token required")
		return
	}

	var volumes []store.Volume
	if s.tokens != nil {
		volumes = s.store.ListVolumesByOwner(principal)
	} else {
		volumes = s.store.ListVolumes()
	}
	respondJSON(w, http.StatusOK, volumes)
}

func (s *Server) handleGetVolume(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "volumeID")
	v, err := s.store.GetVolume(id)
	if err != nil {
		if errors.Is(err, store.ErrVolumeNotFound) {
			respondError(w, http.StatusNotFound, "not_found", "volume not found")
			return
		}
		respondError(w, http.StatusInternalServerError, "store_error", err.Error())
		return
	}

	if principal, ok := principalFromContext(r.Context()); s.tokens != nil {
		if !ok {
			respondError(w, http.StatusUnauthorized, "unauthorized", "token required")
			return
		}
		if v.OwnerPrincipal != principal {
			respondError(w, http.StatusForbidden, "principal_mismatch", "principal not authorised for this volume")
			return
		}
	}

	respondJSON(w, http.StatusOK, v)
}

type attachRequest struct {
	Principal        string `json:"principal"`
	SessionID        string `json:"session_id"`
	ConsumerEndpoint string `json:"consumer_endpoint"`
}

func (s *Server) handleAttachVolume(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "volumeID")
	var req attachRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid_payload", "unable to decode request body")
		return
	}
	principal, havePrincipal := principalFromContext(r.Context())
	if s.tokens != nil && !havePrincipal {
		respondError(w, http.StatusUnauthorized, "unauthorized", "token required")
		return
	}
	vol, err := s.store.GetVolume(id)
	if err != nil {
		if errors.Is(err, store.ErrVolumeNotFound) {
			respondError(w, http.StatusNotFound, "not_found", "volume not found")
			return
		}
		respondError(w, http.StatusInternalServerError, "store_error", err.Error())
		return
	}
	if req.Principal == "" {
		if s.tokens != nil {
			req.Principal = principal
		} else {
			respondError(w, http.StatusBadRequest, "missing_principal", "principal is required")
			return
		}
	}
	if req.Principal != vol.OwnerPrincipal {
		respondError(w, http.StatusForbidden, "principal_mismatch", "principal not authorised for this volume")
		return
	}
	if req.SessionID == "" {
		req.SessionID = "sess-" + strings.ToLower(uuid.NewString()[:8])
	}

	vol.AttachState = mountStateAttached
	vol.MountHandle.State = mountStateAttached
	vol.AttachSession = &store.Session{
		SessionID:        req.SessionID,
		Principal:        req.Principal,
		ConsumerEndpoint: req.ConsumerEndpoint,
		AttachedAt:       time.Now().UTC(),
	}

	persisted, err := s.store.PutVolume(vol)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "store_error", err.Error())
		return
	}

	respondJSON(w, http.StatusOK, persisted)
}

type detachRequest struct {
	SessionID string `json:"session_id"`
}

func (s *Server) handleDetachVolume(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "volumeID")
	var req detachRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		respondError(w, http.StatusBadRequest, "invalid_payload", "unable to decode request body")
		return
	}
	vol, err := s.store.GetVolume(id)
	if err != nil {
		if errors.Is(err, store.ErrVolumeNotFound) {
			respondError(w, http.StatusNotFound, "not_found", "volume not found")
			return
		}
		respondError(w, http.StatusInternalServerError, "store_error", err.Error())
		return
	}

	if principal, ok := principalFromContext(r.Context()); s.tokens != nil {
		if !ok {
			respondError(w, http.StatusUnauthorized, "unauthorized", "token required")
			return
		}
		if vol.OwnerPrincipal != principal {
			respondError(w, http.StatusForbidden, "principal_mismatch", "principal not authorised for this volume")
			return
		}
	}

	vol.AttachState = mountStateAvailable
	vol.MountHandle.State = mountStateAvailable
	vol.AttachSession = nil

	persisted, err := s.store.PutVolume(vol)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "store_error", err.Error())
		return
	}
	respondJSON(w, http.StatusOK, persisted)
}

func (s *Server) handleDeleteVolume(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "volumeID")
	if principal, ok := principalFromContext(r.Context()); s.tokens != nil {
		if !ok {
			respondError(w, http.StatusUnauthorized, "unauthorized", "token required")
			return
		}
		vol, err := s.store.GetVolume(id)
		if err != nil {
			if errors.Is(err, store.ErrVolumeNotFound) {
				respondError(w, http.StatusNotFound, "not_found", "volume not found")
				return
			}
			respondError(w, http.StatusInternalServerError, "store_error", err.Error())
			return
		}
		if vol.OwnerPrincipal != principal {
			respondError(w, http.StatusForbidden, "principal_mismatch", "principal not authorised for this volume")
			return
		}
	}

	if err := s.store.DeleteVolume(id); err != nil {
		if errors.Is(err, store.ErrVolumeNotFound) {
			respondError(w, http.StatusNotFound, "not_found", "volume not found")
			return
		}
		respondError(w, http.StatusInternalServerError, "store_error", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func respondJSON(w http.ResponseWriter, status int, payload interface{}) {
	w.WriteHeader(status)
	if payload == nil {
		return
	}
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

type createSnapshotRequest struct {
	Note string `json:"note,omitempty"`
}

type snapshotResponse store.Snapshot

type createCheckpointRequest struct {
	VolumeIDs []string `json:"volume_ids"`
	Note      string   `json:"note,omitempty"`
}

type checkpointResponse store.Checkpoint

func (s *Server) handleCreateSnapshot(w http.ResponseWriter, r *http.Request) {
	volumeID := chi.URLParam(r, "volumeID")
	principal, ok := principalFromContext(r.Context())
	if s.tokens != nil && !ok {
		respondError(w, http.StatusUnauthorized, "unauthorized", "token required")
		return
	}

	vol, err := s.store.GetVolume(volumeID)
	if err != nil {
		if errors.Is(err, store.ErrVolumeNotFound) {
			respondError(w, http.StatusNotFound, "not_found", "volume not found")
			return
		}
		respondError(w, http.StatusInternalServerError, "store_error", err.Error())
		return
	}
	if s.tokens != nil && vol.OwnerPrincipal != principal {
		respondError(w, http.StatusForbidden, "principal_mismatch", "principal not authorised for this volume")
		return
	}

	var req createSnapshotRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		respondError(w, http.StatusBadRequest, "invalid_payload", "unable to decode request body")
		return
	}

	snapshot := store.Snapshot{
		SnapshotID: "snap-" + strings.ToLower(uuid.NewString()[:8]),
		VolumeID:   volumeID,
		CreatedAt:  time.Now().UTC(),
		Note:       req.Note,
	}
	persisted, err := s.store.AddSnapshot(volumeID, snapshot)
	if err != nil {
		if errors.Is(err, store.ErrVolumeNotFound) {
			respondError(w, http.StatusNotFound, "not_found", "volume not found")
			return
		}
		respondError(w, http.StatusInternalServerError, "store_error", err.Error())
		return
	}

	respondJSON(w, http.StatusCreated, persisted)
}

func (s *Server) handleListSnapshots(w http.ResponseWriter, r *http.Request) {
	volumeID := chi.URLParam(r, "volumeID")
	principal, ok := principalFromContext(r.Context())
	if s.tokens != nil && !ok {
		respondError(w, http.StatusUnauthorized, "unauthorized", "token required")
		return
	}

	vol, err := s.store.GetVolume(volumeID)
	if err != nil {
		if errors.Is(err, store.ErrVolumeNotFound) {
			respondError(w, http.StatusNotFound, "not_found", "volume not found")
			return
		}
		respondError(w, http.StatusInternalServerError, "store_error", err.Error())
		return
	}
	if s.tokens != nil && vol.OwnerPrincipal != principal {
		respondError(w, http.StatusForbidden, "principal_mismatch", "principal not authorised for this volume")
		return
	}

	snaps := s.store.ListSnapshots(volumeID)
	respondJSON(w, http.StatusOK, snaps)
}

func (s *Server) handleCreateCheckpoint(w http.ResponseWriter, r *http.Request) {
	principal, ok := principalFromContext(r.Context())
	if s.tokens != nil && !ok {
		respondError(w, http.StatusUnauthorized, "unauthorized", "token required")
		return
	}

	var req createCheckpointRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid_payload", "unable to decode request body")
		return
	}

	volumeIDs := req.VolumeIDs
	if len(volumeIDs) == 0 {
		if s.tokens != nil {
			volumeIDs = make([]string, 0)
			for _, v := range s.store.ListVolumesByOwner(principal) {
				volumeIDs = append(volumeIDs, v.VolumeID)
			}
		} else {
			for _, v := range s.store.ListVolumes() {
				volumeIDs = append(volumeIDs, v.VolumeID)
			}
		}
	}

	snapshotIDs := make([]string, 0, len(volumeIDs))
	for _, vid := range volumeIDs {
		vol, err := s.store.GetVolume(vid)
		if err != nil {
			respondError(w, http.StatusBadRequest, "invalid_volume", fmt.Sprintf("unknown volume %s", vid))
			return
		}
		if s.tokens != nil && vol.OwnerPrincipal != principal {
			respondError(w, http.StatusForbidden, "principal_mismatch", fmt.Sprintf("principal not authorised for volume %s", vid))
			return
		}
		latest, ok := s.store.LatestSnapshot(vid)
		if !ok {
			created, err := s.store.AddSnapshot(vid, store.Snapshot{
				SnapshotID: "snap-" + strings.ToLower(uuid.NewString()[:8]),
				VolumeID:   vid,
				CreatedAt:  time.Now().UTC(),
				Note:       "auto-generated for checkpoint",
			})
			if err != nil {
				respondError(w, http.StatusInternalServerError, "store_error", err.Error())
				return
			}
			latest = created
		}
		snapshotIDs = append(snapshotIDs, latest.SnapshotID)
	}

	manifest := store.Checkpoint{
		ManifestID:  "chk-" + strings.ToLower(uuid.NewString()[:8]),
		SnapshotIDs: snapshotIDs,
		CreatedAt:   time.Now().UTC(),
		Note:        req.Note,
	}

	persisted, err := s.store.PutCheckpoint(manifest)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "store_error", err.Error())
		return
	}

	respondJSON(w, http.StatusCreated, persisted)
}

func (s *Server) handleListCheckpoints(w http.ResponseWriter, r *http.Request) {
	principal, ok := principalFromContext(r.Context())
	if s.tokens != nil && !ok {
		respondError(w, http.StatusUnauthorized, "unauthorized", "token required")
		return
	}

	manifests := s.store.ListCheckpoints()
	if s.tokens != nil {
		// Filter manifests to only those referencing the caller's volumes.
		allowed := make([]store.Checkpoint, 0, len(manifests))
		owned := map[string]struct{}{}
		for _, v := range s.store.ListVolumesByOwner(principal) {
			owned[v.VolumeID] = struct{}{}
		}
		for _, cp := range manifests {
			include := true
			for _, sid := range cp.SnapshotIDs {
				if vid, ok := s.store.VolumeIDForSnapshot(sid); ok {
					if _, ownedVol := owned[vid]; !ownedVol {
						include = false
						break
					}
				}
			}
			if include {
				allowed = append(allowed, cp)
			}
		}
		manifests = allowed
	}

	respondJSON(w, http.StatusOK, manifests)
}

func respondError(w http.ResponseWriter, status int, code, message string) {
	respondJSON(w, status, errorResponse{Error: code, Message: message})
}

func jsonMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		next.ServeHTTP(w, r)
	})
}
