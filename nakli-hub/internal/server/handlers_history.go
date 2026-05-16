package server

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/NakliTechie/private-mesh/nakli-hub/internal/storage"
)

type historyAppendReq struct {
	StreamID string `json:"stream_id"`
	Event    struct {
		Kind               string           `json:"kind"`
		PayloadCiphertext  string           `json:"payload_ciphertext"`
		PayloadMetadata    json.RawMessage  `json:"payload_metadata"`
		CausalDependencies []string         `json:"causal_dependencies"`
		VectorClock        map[string]int64 `json:"vector_clock"`
		PreviousEventHash  string           `json:"previous_event_hash"` // base64; empty for first append
	} `json:"event"`
}

type historyAppendResp struct {
	EventID        string `json:"event_id"`
	EventHash      string `json:"event_hash"` // base64
	SequenceNumber int64  `json:"sequence_number"`
}

func (s *Server) handleHistoryAppend(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var req historyAppendReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, r, http.StatusBadRequest, ErrBadRequest, "request body is not valid JSON", false)
		return
	}
	if req.StreamID == "" || req.Event.Kind == "" {
		writeError(w, r, http.StatusBadRequest, ErrBadRequest, "stream_id and event.kind are required", false)
		return
	}
	if err := s.checkAuth(w, r, scopeRequirement{
		Primitive: "history",
		Operation: "write",
	}); err != nil {
		return
	}
	ciphertext, err := base64.StdEncoding.DecodeString(req.Event.PayloadCiphertext)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, ErrBadRequest, "event.payload_ciphertext is not valid base64", false)
		return
	}
	if int64(len(ciphertext)) > s.cfg.Storage.MaxEventSizeBytes {
		writeError(w, r, http.StatusBadRequest, ErrBadRequest, "event payload exceeds max_event_size_bytes", false)
		return
	}
	var prevHash []byte
	if req.Event.PreviousEventHash != "" {
		prevHash, err = base64.StdEncoding.DecodeString(req.Event.PreviousEventHash)
		if err != nil {
			writeError(w, r, http.StatusBadRequest, ErrBadRequest, "previous_event_hash is not valid base64", false)
			return
		}
	}

	eventID := newULID()
	blobPath, err := s.store.WriteBlob(storage.HistoryNamespace, eventID, ciphertext, s.cfg.Storage.FsyncWrites)
	if err != nil {
		s.logger.Error("WriteBlob failed", "err", err)
		writeError(w, r, http.StatusInternalServerError, ErrUnavailable, "blob persist failed", true)
		return
	}

	g := grantFromCtx(ctx)
	in := storage.HistoryAppendInput{
		StreamID:            req.StreamID,
		Kind:                req.Event.Kind,
		PayloadCiphertext:   ciphertext,
		PayloadMetadata:     rawOrEmpty(req.Event.PayloadMetadata),
		CausalDependencies:  jsonStringArray(req.Event.CausalDependencies),
		VectorClock:         jsonStringMap(req.Event.VectorClock),
		PreviousEventHash:   prevHash,
		AppendedByPrincipal: g.Identifier.IssuedByPrincipal,
		AppendedByGrantID:   g.Identifier.GrantID,
	}
	res, err := s.store.AppendHistoryEvent(ctx, in, blobPath, eventID)
	if err != nil {
		if errors.Is(err, storage.ErrHistoryConflict) {
			writeError(w, r, http.StatusConflict, ErrConflict, "previous_event_hash does not match current stream head", false)
			return
		}
		s.logger.Error("AppendHistoryEvent failed", "err", err)
		writeError(w, r, http.StatusInternalServerError, ErrUnavailable, "event persist failed", true)
		return
	}
	writeSuccess(w, r, http.StatusOK, historyAppendResp{
		EventID:        res.EventID,
		EventHash:      base64.StdEncoding.EncodeToString(res.EventHash),
		SequenceNumber: res.SequenceNumber,
	}, FreshnessNow(s.now()))
}

type historyEventOut struct {
	EventID            string           `json:"event_id"`
	Kind               string           `json:"kind"`
	SequenceNumber     int64            `json:"sequence_number"`
	PayloadCiphertext  string           `json:"payload_ciphertext"`
	PayloadMetadata    json.RawMessage  `json:"payload_metadata,omitempty"`
	CausalDependencies []string         `json:"causal_dependencies"`
	VectorClock        map[string]int64 `json:"vector_clock"`
	PreviousEventHash  string           `json:"previous_event_hash"` // base64; empty for first event
	EventHash          string           `json:"event_hash"`          // base64
	AppendedAt         time.Time        `json:"appended_at"`
	AppendedByPrincipal string          `json:"appended_by_principal"`
	AppendedByDevice    string          `json:"appended_by_device,omitempty"`
}

type historyReadResp struct {
	Events []historyEventOut `json:"events"`
	More   bool              `json:"more"`
}

func (s *Server) handleHistoryRead(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	streamID := r.PathValue("stream_id")
	if streamID == "" {
		writeError(w, r, http.StatusBadRequest, ErrBadRequest, "stream_id is required", false)
		return
	}
	if err := s.checkAuth(w, r, scopeRequirement{
		Primitive: "history",
		Operation: "read",
	}); err != nil {
		return
	}
	since := r.URL.Query().Get("since")
	limit := 100
	if l := r.URL.Query().Get("limit"); l != "" {
		if n := atoiSafe(l, 100); n > 0 && n <= 1000 {
			limit = n
		}
	}
	rows, more, err := s.store.ReadStream(ctx, storage.HistoryNamespace, streamID, storage.ReadOptions{
		SinceEventID: since,
		Limit:        limit,
	})
	if err != nil {
		if err == storage.ErrNotFound {
			writeError(w, r, http.StatusNotFound, ErrNotFound, "since event_id not found in this stream", false)
			return
		}
		s.logger.Error("ReadStream failed", "err", err)
		writeError(w, r, http.StatusInternalServerError, ErrUnavailable, "stream read failed", true)
		return
	}
	out := historyReadResp{Events: make([]historyEventOut, 0, len(rows)), More: more}
	for _, ev := range rows {
		var meta json.RawMessage
		if ev.PayloadMetadata != "" {
			meta = json.RawMessage(ev.PayloadMetadata)
		}
		var vc map[string]int64
		if ev.VectorClock != "" {
			_ = json.Unmarshal([]byte(ev.VectorClock), &vc)
		}
		var deps []string
		if ev.CausalDependencies != "" {
			_ = json.Unmarshal([]byte(ev.CausalDependencies), &deps)
		}
		out.Events = append(out.Events, historyEventOut{
			EventID:             ev.EventID,
			Kind:                ev.Kind,
			SequenceNumber:      ev.SequenceNumber,
			PayloadCiphertext:   base64.StdEncoding.EncodeToString(ev.PayloadCiphertext),
			PayloadMetadata:     meta,
			CausalDependencies:  deps,
			VectorClock:         vc,
			PreviousEventHash:   base64IfPresent(ev.PreviousEventHash),
			EventHash:           base64IfPresent(ev.EventHash),
			AppendedAt:          ev.AppendedAt,
			AppendedByPrincipal: ev.AppendedByPrincipal,
			AppendedByDevice:    ev.AppendedByDevice,
		})
	}
	writeSuccess(w, r, http.StatusOK, out, FreshnessNow(s.now()))
}

type historyVerifyResp struct {
	Verified        bool   `json:"verified"`
	Length          int64  `json:"length"`
	HeadHash        string `json:"head_hash"`
	BrokenAtEventID string `json:"broken_at_event_id,omitempty"`
}

func (s *Server) handleHistoryVerify(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	streamID := r.PathValue("stream_id")
	if streamID == "" {
		writeError(w, r, http.StatusBadRequest, ErrBadRequest, "stream_id is required", false)
		return
	}
	if err := s.checkAuth(w, r, scopeRequirement{
		Primitive: "history",
		Operation: "read",
	}); err != nil {
		return
	}
	res, err := s.store.VerifyHistoryChain(ctx, streamID)
	if err != nil {
		s.logger.Error("VerifyHistoryChain failed", "err", err)
		writeError(w, r, http.StatusInternalServerError, ErrUnavailable, "verify failed", true)
		return
	}
	writeSuccess(w, r, http.StatusOK, historyVerifyResp{
		Verified:        res.Verified,
		Length:          res.Length,
		HeadHash:        base64IfPresent(res.HeadHash),
		BrokenAtEventID: res.BrokenAtEventID,
	}, FreshnessNow(s.now()))
}
