// Package local implements the Local Network transport's discovery layer:
// mDNS announce + browse for `_nakli-fabric._tcp.local.` per
// docs/specs/local-network-spec-001-v1.1.md.
//
// The Hub embeds Announcer + Browser to surface discovered peers on
// /sync/peers. nakli-local-bridge does the same for browser tools that
// can't speak mDNS directly. Peer-to-peer HTTPS / WebRTC connection
// establishment lands at M7.x; this package handles discovery only.
package local

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/grandcat/zeroconf"
)

var stderr = os.Stderr

// ServiceType is the DNS-SD service name reserved for the fabric.
const ServiceType = "_nakli-fabric._tcp"

// Domain is the mDNS link-local domain.
const Domain = "local."

// Peer is the in-process shape of a discovered peer.
type Peer struct {
	TransportID  string    `json:"transport_id"`
	PrincipalID  string    `json:"principal_id"`
	DeviceID     string    `json:"device_id,omitempty"`
	HubID        string    `json:"hub_id,omitempty"`
	Version      string    `json:"version"`
	Capabilities []string  `json:"capabilities"`
	Host         string    `json:"host"`
	Port         int       `json:"port"`
	URL          string    `json:"url,omitempty"`
	DiscoveredAt time.Time `json:"discovered_at"`
	LastSeenAt   time.Time `json:"last_seen_at"`
}

// AnnounceSpec is the input to NewAnnouncer.
type AnnounceSpec struct {
	// Instance name; defaults to a synthetic string from TransportID +
	// PrincipalID. Must be unique on the network.
	Instance string
	// Port the transport listens on. Required.
	Port int
	// TXT records — Hub callers populate principal_id / transport_id /
	// hub_id / version / capabilities per the spec.
	TXT []string
	// Hostnames the consumer can reach this peer at. Defaults to
	// `<short>.local.` per zeroconf.
	HostName string
	// IPs binds advertised addresses. nil = announce on every interface.
	IPs []net.IP
}

// Announcer manages an mDNS service registration. Close on shutdown to
// send the goodbye packet.
type Announcer struct {
	server *zeroconf.Server
}

// NewAnnouncer registers the service on the link-local network. The
// goroutine that handles re-announcement is owned by zeroconf.Server;
// call Close to stop.
func NewAnnouncer(spec AnnounceSpec) (*Announcer, error) {
	if spec.Port <= 0 {
		return nil, errors.New("local.NewAnnouncer: port is required")
	}
	if spec.Instance == "" {
		spec.Instance = "nakli-" + shortRand()
	}
	srv, err := zeroconf.RegisterProxy(
		spec.Instance,
		ServiceType,
		Domain,
		spec.Port,
		spec.HostName,
		ipsToStrings(spec.IPs),
		spec.TXT,
		nil, // ifaces: nil = all
	)
	if err != nil {
		// Some platforms forbid RegisterProxy without an explicit hostname;
		// fall back to Register if HostName isn't set.
		if spec.HostName == "" {
			srv, err = zeroconf.Register(spec.Instance, ServiceType, Domain, spec.Port, spec.TXT, nil)
		}
		if err != nil {
			return nil, fmt.Errorf("local.NewAnnouncer: %w", err)
		}
	}
	srv.TTL(120)
	return &Announcer{server: srv}, nil
}

// Close shuts down the registration; zeroconf sends a goodbye record.
func (a *Announcer) Close() {
	if a == nil || a.server == nil {
		return
	}
	a.server.Shutdown()
}

// Browser collects peers seen on the network. Run periodically (every
// few seconds is fine); pass a fresh ctx with a deadline. Peer entries
// older than `staleAfter` are evicted.
type Browser struct {
	mu          sync.Mutex
	peers       map[string]*Peer
	staleAfter  time.Duration
	observers   []chan []Peer
	browseCtx   context.Context
	browseCxl   context.CancelFunc
	// excludeTransportID lets a consumer hide its own announcement from
	// the discovered-peers list.
	excludeTransportID string
}

// NewBrowser builds a Browser with the default 180s staleness budget. Call
// Start to begin a background browse loop; call Stop on shutdown.
func NewBrowser() *Browser {
	return &Browser{
		peers:      map[string]*Peer{},
		staleAfter: 180 * time.Second,
	}
}

// SetExcludeTransportID hides one's own announcement from results.
func (b *Browser) SetExcludeTransportID(id string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.excludeTransportID = id
}

// Peers returns a snapshot of currently-known peers (deduped by
// transport_id), sorted by discovery time.
func (b *Browser) Peers() []Peer {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := time.Now()
	out := make([]Peer, 0, len(b.peers))
	for tid, p := range b.peers {
		if now.Sub(p.LastSeenAt) > b.staleAfter {
			delete(b.peers, tid)
			continue
		}
		if tid == b.excludeTransportID {
			continue
		}
		out = append(out, *p)
	}
	return out
}

// Observe registers a channel that receives the peer list on every
// change. The channel is buffered to 4; slow consumers drop updates.
func (b *Browser) Observe(c chan []Peer) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.observers = append(b.observers, c)
}

// Start runs a long-lived background browse. zeroconf streams entries into
// the channel for the lifetime of the context; on cancel it closes the
// channel exactly once. Returns immediately; call Stop to terminate.
func (b *Browser) Start(parent context.Context) error {
	resolver, err := zeroconf.NewResolver(nil)
	if err != nil {
		return fmt.Errorf("local.Browser.Start: %w", err)
	}
	ctx, cxl := context.WithCancel(parent)
	b.browseCtx, b.browseCxl = ctx, cxl

	entries := make(chan *zeroconf.ServiceEntry, 32)
	go func() {
		for e := range entries {
			b.absorb(e)
		}
	}()
	// Single long-lived Browse. Calling Browse in a loop with the same
	// channel double-closes on the second iteration (zeroconf v1.0.0
	// closes entries when ctx is done).
	go func() {
		if err := resolver.Browse(ctx, ServiceType, Domain, entries); err != nil {
			fmt.Fprintln(stderr, "local.Browser: Browse:", err)
		}
	}()
	return nil
}

// Stop cancels the background browse loop.
func (b *Browser) Stop() {
	if b.browseCxl != nil {
		b.browseCxl()
	}
}

func (b *Browser) absorb(e *zeroconf.ServiceEntry) {
	if e == nil {
		return
	}
	p := &Peer{
		Host:         strings.TrimSuffix(e.HostName, "."),
		Port:         e.Port,
		Version:      "naklimesh/1.0",
		DiscoveredAt: time.Now(),
		LastSeenAt:   time.Now(),
	}
	for _, kv := range e.Text {
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		switch k {
		case "principal_id":
			p.PrincipalID = v
		case "device_id":
			p.DeviceID = v
		case "transport_id":
			p.TransportID = v
		case "hub_id":
			p.HubID = v
		case "version":
			p.Version = v
		case "capabilities":
			p.Capabilities = strings.Split(v, ",")
		case "url":
			p.URL = v
		}
	}
	if len(e.AddrIPv4) > 0 && p.URL == "" {
		p.URL = fmt.Sprintf("http://%s:%d", e.AddrIPv4[0].String(), e.Port)
	}
	b.mu.Lock()
	if p.TransportID == "" {
		// Use host:port as a stable key for peers that didn't supply a transport_id.
		p.TransportID = fmt.Sprintf("%s:%d", p.Host, p.Port)
	}
	if existing, ok := b.peers[p.TransportID]; ok {
		existing.LastSeenAt = time.Now()
		// Preserve discovered-at; refresh metadata in case TXT changed.
		existing.URL = p.URL
		existing.Capabilities = p.Capabilities
		existing.Version = p.Version
	} else {
		b.peers[p.TransportID] = p
	}
	snapshot := make([]Peer, 0, len(b.peers))
	for tid, pp := range b.peers {
		if tid == b.excludeTransportID {
			continue
		}
		snapshot = append(snapshot, *pp)
	}
	observers := append([]chan []Peer(nil), b.observers...)
	b.mu.Unlock()
	for _, c := range observers {
		select {
		case c <- snapshot:
		default:
		}
	}
}

// --- helpers ---

func ipsToStrings(ips []net.IP) []string {
	out := make([]string, 0, len(ips))
	for _, ip := range ips {
		out = append(out, ip.String())
	}
	return out
}

func shortRand() string {
	// Fall back to time-based suffix; consumers usually supply Instance.
	return fmt.Sprintf("%x", time.Now().UnixNano()&0xFFFFFF)
}
