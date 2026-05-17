// Command nakli-local-bridge is the standalone mDNS bridge daemon. It
// announces this device's presence on the local network, browses for other
// fabric peers, and exposes a small HTTP surface that browser tools query
// to learn about local peers (browsers cannot speak mDNS directly).
//
// Spec: docs/specs/local-network-spec-001-v1.1.md §"Browser-specific
// implementation".
//
// M7 ships the discovery half: GET /local/peers returns the current peer
// list; GET /local/health is a tiny "is the bridge alive" probe. WebSocket
// peer-list streaming and POST /local/signal (WebRTC offer/answer relay)
// land at M7.x.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/NakliTechie/private-mesh/fabric-sdk-go/local"
)

// BinaryVersion is set via -ldflags at release time.
var BinaryVersion = "0.1.0-alpha.0"

const defaultListen = "127.0.0.1:7849"

func main() {
	listen := flag.String("listen", defaultListen, "address the HTTP surface binds to")
	announce := flag.Bool("announce", true, "also announce this bridge on mDNS so other peers can see it")
	instance := flag.String("instance", "nakli-local-bridge", "mDNS instance name when --announce")
	announcePort := flag.Int("announce-port", 7849, "port to advertise in the mDNS TXT (typically same as --listen's port)")
	verbose := flag.Bool("verbose", false, "log peer changes to stderr")
	flag.Parse()

	browser := local.NewBrowser()
	if err := browser.Start(context.Background()); err != nil {
		fmt.Fprintln(os.Stderr, "nakli-local-bridge: browse failed:", err)
		os.Exit(1)
	}
	defer browser.Stop()

	var announcer *local.Announcer
	if *announce {
		a, err := local.NewAnnouncer(local.AnnounceSpec{
			Instance: *instance,
			Port:     *announcePort,
			TXT: []string{
				"version=naklimesh/1.0",
				"transport_id=bridge-" + *instance,
				"capabilities=discovery",
				"url=http://" + *listen,
			},
		})
		if err != nil {
			fmt.Fprintln(os.Stderr, "nakli-local-bridge: announce failed:", err)
		} else {
			announcer = a
			browser.SetExcludeTransportID("bridge-" + *instance)
		}
	}

	if *verbose {
		ch := make(chan []local.Peer, 8)
		browser.Observe(ch)
		go func() {
			for snapshot := range ch {
				fmt.Fprintf(os.Stderr, "peers: %d total\n", len(snapshot))
				for _, p := range snapshot {
					fmt.Fprintf(os.Stderr, "  %s @ %s (%s)\n", p.HubID, p.URL, p.Version)
				}
			}
		}()
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /local/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{
			"ok": true,
			"data": map[string]any{
				"binary":     BinaryVersion,
				"version":    "naklimesh/1.0",
				"announcing": announcer != nil,
				"instance":   *instance,
				"port":       *announcePort,
			},
		})
	})
	mux.HandleFunc("GET /local/peers", func(w http.ResponseWriter, r *http.Request) {
		peers := browser.Peers()
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":   true,
			"data": map[string]any{"peers": peers},
		})
	})
	// Forward-compat hooks — surface clear 501s so consumers don't crash.
	mux.HandleFunc("POST /local/signal", notImplemented("M7.x — WebRTC signaling relay"))
	mux.HandleFunc("/local/peers/observe", notImplemented("M7.x — WebSocket peer-list streaming"))

	srv := &http.Server{
		Addr:              *listen,
		Handler:           withCORS(mux),
		ReadHeaderTimeout: 5 * time.Second,
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	go func() {
		<-ctx.Done()
		if announcer != nil {
			announcer.Close()
		}
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	fmt.Fprintf(os.Stderr, "nakli-local-bridge %s listening on http://%s\n", BinaryVersion, *listen)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		fmt.Fprintln(os.Stderr, "nakli-local-bridge: listen:", err)
		os.Exit(1)
	}
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func notImplemented(msg string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusNotImplemented, map[string]any{
			"ok":    false,
			"error": map[string]string{"code": "not_implemented", "message": msg},
		})
	}
}

// withCORS is permissive on purpose — the bridge talks to localhost browser
// tabs on arbitrary origins.
func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
