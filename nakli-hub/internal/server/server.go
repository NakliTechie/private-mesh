package server

import (
	"log/slog"
	"net/http"
	"sync"
	"time"

	lru "github.com/hashicorp/golang-lru/v2"

	"github.com/NakliTechie/private-mesh/fabric-sdk-go/bridge"
	"github.com/NakliTechie/private-mesh/fabric-sdk-go/local"
	"github.com/NakliTechie/private-mesh/nakli-hub/internal/config"
	"github.com/NakliTechie/private-mesh/nakli-hub/internal/hubid"
	"github.com/NakliTechie/private-mesh/nakli-hub/internal/storage"
)

// Bounds on the in-memory rate-limit + discharge-cache LRU maps. Before
// these were unbounded plain maps — an attacker could mint many
// short-lived grants, hit the rate-bucket / discharge code path once
// per grant, and grow the maps without limit. 10k entries is generous
// for legitimate concurrent grant volume on a single Hub.
const (
	rateBucketLRUSize    = 10_000
	dischargeCacheLRUSize = 10_000
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
	// LRU-capped so an attacker minting many grants cannot grow the map
	// unbounded. The LRU is internally thread-safe; rateMu only guards
	// the get-or-create critical section.
	rateMu      sync.Mutex
	rateBuckets *lru.Cache[string, *rateBucket]

	// dischargeCache stores verified discharge macaroons by third-party
	// caveat id (an attacker-controlled URL string). LRU-capped for the
	// same reason as rateBuckets.
	dischargeCache *lru.Cache[string, cachedDischarge]

	// peerURLs is the list of remote peers `/health` probes for the `degraded`
	// flag. Real multi-peer sync lands at M7; M3 uses this only to satisfy
	// conformance test 26.
	peerMu   sync.Mutex
	peerURLs []string

	// bridge is the Bridge adapter registry; nil = no adapters → /bridge/call
	// keeps returning 501 for caveat-passing calls (the M3 behavior). The
	// Hub binary wires this up at startup.
	bridge *bridge.Registry

	// localBrowser is the mDNS browser the Hub uses to surface peers on
	// /sync/peers (M7). nil = no mDNS (tests that don't want network
	// access); /sync/peers returns an empty list.
	localBrowser *local.Browser

	// bucketProxyClient is the HTTP client used to proxy /v1/crate/object/*
	// requests to upstream R2 / Hetzner / B2 / AWS S3. nil = http.DefaultClient.
	// Tests inject an httptest.Server-backed client to point at the fake-R2
	// fixture without leaving the process.
	bucketProxyClient *http.Client
}

// SetBucketProxyClient installs the outbound HTTP client used by the crate
// bucket-proxy handlers to call upstream S3-API providers. Pass nil to reset
// to http.DefaultClient. Used by tests to redirect upstream calls to a fake-R2
// httptest.Server.
func (s *Server) SetBucketProxyClient(c *http.Client) { s.bucketProxyClient = c }

// New constructs a Server. cfg, store, and identity must be initialized.
// binaryVersion is the runtime binary version string (e.g. "0.1.0-alpha.0").
func New(cfg *config.Config, store *storage.Store, id *hubid.Identity, logger *slog.Logger, binaryVersion string) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	// The LRU constructor only fails on size<=0, so a panic-on-error
	// is appropriate (and shorter than threading the error to callers).
	rb, err := lru.New[string, *rateBucket](rateBucketLRUSize)
	if err != nil {
		panic("server.New: rateBuckets LRU: " + err.Error())
	}
	dc, err := lru.New[string, cachedDischarge](dischargeCacheLRUSize)
	if err != nil {
		panic("server.New: dischargeCache LRU: " + err.Error())
	}
	return &Server{
		cfg:            cfg,
		store:          store,
		hubID:          id,
		logger:         logger,
		now:            time.Now,
		startAt:        time.Now(),
		binVer:         binaryVersion,
		rateBuckets:    rb,
		dischargeCache: dc,
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

// SetBridgeRegistry installs the Bridge adapter registry. Pass nil to clear
// (in which case /bridge/call falls back to the 501 "execution lands at M5.5"
// path). Call before serving traffic; the registry is not goroutine-protected
// against late changes.
func (s *Server) SetBridgeRegistry(r *bridge.Registry) { s.bridge = r }

// BridgeRegistry returns the installed registry, or nil. Used by tests.
func (s *Server) BridgeRegistry() *bridge.Registry { return s.bridge }

// SetLocalBrowser installs the mDNS browser the Hub queries for /sync/peers.
// The Hub binary wires this up at serve time when local network is enabled.
func (s *Server) SetLocalBrowser(b *local.Browser) { s.localBrowser = b }
