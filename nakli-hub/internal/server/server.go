package server

import (
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/NakliTechie/private-mesh/nakli-hub/internal/config"
	"github.com/NakliTechie/private-mesh/nakli-hub/internal/hubid"
	"github.com/NakliTechie/private-mesh/nakli-hub/internal/storage"
)

// Server is the Hub's HTTP application. Build with New; mount Handler() on
// an *http.Server.
type Server struct {
	cfg     *config.Config
	store   *storage.Store
	hubID   *hubid.Identity
	logger  *slog.Logger
	now     func() time.Time
	startAt time.Time
	binVer  string

	// rateBuckets tracks per-grant token buckets for the `rate` caveat.
	rateMu      sync.Mutex
	rateBuckets map[string]*rateBucket

	// dischargeCache stores verified discharge macaroons by third-party caveat id.
	dischargeMu    sync.Mutex
	dischargeCache map[string]cachedDischarge

	// peerURLs is the list of remote peers `/health` probes for the `degraded`
	// flag. Real multi-peer sync lands at M7; M3 uses this only to satisfy
	// conformance test 26.
	peerMu   sync.Mutex
	peerURLs []string
}

// New constructs a Server. cfg, store, and identity must be initialized.
// binaryVersion is the runtime binary version string (e.g. "0.1.0-alpha.0").
func New(cfg *config.Config, store *storage.Store, id *hubid.Identity, logger *slog.Logger, binaryVersion string) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	return &Server{
		cfg:            cfg,
		store:          store,
		hubID:          id,
		logger:         logger,
		now:            time.Now,
		startAt:        time.Now(),
		binVer:         binaryVersion,
		rateBuckets:    map[string]*rateBucket{},
		dischargeCache: map[string]cachedDischarge{},
	}
}

// WithClock overrides the clock used for response timestamps (testing).
func (s *Server) WithClock(now func() time.Time) *Server {
	s.now = now
	s.startAt = now()
	return s
}

// Handler returns the composed http.Handler ready to serve.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	s.routes(mux)
	// Outer middleware order: log first so it sees everything; CORS so
	// preflights work; then per-route handlers.
	return s.logMiddleware(corsMiddleware(mux))
}

// HubID returns the Hub's own ULID. Exposed for tests.
func (s *Server) HubID() string { return s.hubID.HubID }

// MacaroonRootKey returns the Hub's macaroon HMAC root key. Exposed for tests
// that need to mint Grants against this Hub.
func (s *Server) MacaroonRootKey() []byte { return s.hubID.MacaroonRootKey }
