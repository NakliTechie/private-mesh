package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/NakliTechie/private-mesh/nakli-hub/internal/storage"
)

// --- GET /fabric/v1/vault/streams/{namespace} ---

type vaultStreamSummary struct {
	StreamID      string `json:"stream_id"`
	LatestEventID string `json:"latest_event_id"`
	EventCount    int64  `json:"event_count"`
}

type vaultStreamsResp struct {
	Streams []vaultStreamSummary `json:"streams"`
}

func (s *Server) handleVaultListStreams(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	namespace := r.PathValue("namespace")
	if namespace == "" {
		writeError(w, r, http.StatusBadRequest, ErrBadRequest, "namespace is required", false)
		return
	}
	if err := s.checkAuth(w, r, scopeRequirement{
		Primitive: "vault",
		Namespace: namespace,
		Operation: "read",
	}); err != nil {
		return
	}
	rows, err := s.store.DB().QueryContext(ctx, `
        SELECT stream_id, COALESCE(head_event_id, ''), event_count
        FROM streams WHERE namespace = ?
        ORDER BY stream_id ASC`,
		namespace,
	)
	if err != nil {
		s.logger.Error("list streams query failed", "err", err)
		writeError(w, r, http.StatusInternalServerError, ErrUnavailable, "list streams failed", true)
		return
	}
	defer rows.Close()
	out := vaultStreamsResp{Streams: []vaultStreamSummary{}}
	for rows.Next() {
		var ss vaultStreamSummary
		if err := rows.Scan(&ss.StreamID, &ss.LatestEventID, &ss.EventCount); err != nil {
			writeError(w, r, http.StatusInternalServerError, ErrUnavailable, "list streams scan failed", true)
			return
		}
		out.Streams = append(out.Streams, ss)
	}
	writeSuccess(w, r, http.StatusOK, out, FreshnessNow(s.now()))
}

// --- POST /fabric/v1/vault/subscribe (SSE) ---

type vaultSubscribeReq struct {
	Namespace    string `json:"namespace"`
	StreamID     string `json:"stream_id"`
	SinceEventID string `json:"since_event_id"`
}

// handleVaultSubscribe streams events via Server-Sent Events. Phase 2b is a
// *polling* implementation: every 500 ms the handler re-reads new rows since
// the last delivered sequence_number. A future pass may swap in a push-based
// pubsub (SQLite has no LISTEN/NOTIFY; polling is portable and bounded).
func (s *Server) handleVaultSubscribe(w http.ResponseWriter, r *http.Request) {
	var req vaultSubscribeReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, r, http.StatusBadRequest, ErrBadRequest, "request body is not valid JSON", false)
		return
	}
	if req.Namespace == "" || req.StreamID == "" {
		writeError(w, r, http.StatusBadRequest, ErrBadRequest, "namespace and stream_id are required", false)
		return
	}
	if err := s.checkAuth(w, r, scopeRequirement{
		Primitive: "vault",
		Namespace: req.Namespace,
		Operation: "subscribe",
	}); err != nil {
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, r, http.StatusInternalServerError, ErrUnavailable, "streaming not supported by this ResponseWriter", true)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	lastEventID := req.SinceEventID
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()

	deliverNew := func() bool {
		events, _, err := s.store.ReadStream(ctx, req.Namespace, req.StreamID, storage.ReadOptions{
			SinceEventID: lastEventID,
			Limit:        1000,
		})
		if err != nil || len(events) == 0 {
			return true
		}
		for _, ev := range events {
			payload, err := json.Marshal(map[string]any{
				"event_id":              ev.EventID,
				"kind":                  ev.Kind,
				"sequence_number":       ev.SequenceNumber,
				"payload_ciphertext":    base64.StdEncoding.EncodeToString(ev.PayloadCiphertext),
				"appended_at":           ev.AppendedAt,
				"appended_by_principal": ev.AppendedByPrincipal,
			})
			if err != nil {
				continue
			}
			if _, err := fmt.Fprintf(w, "id: %s\nevent: vault.event\ndata: %s\n\n", ev.EventID, payload); err != nil {
				return false
			}
			flusher.Flush()
			lastEventID = ev.EventID
		}
		return true
	}

	if !deliverNew() {
		return
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-heartbeat.C:
			if _, err := fmt.Fprintf(w, ": heartbeat %s\n\n", s.now().UTC().Format(time.RFC3339Nano)); err != nil {
				return
			}
			flusher.Flush()
		case <-ticker.C:
			if !deliverNew() {
				return
			}
		}
	}
}
