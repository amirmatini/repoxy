package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"repoxy/internal/admin"
	"repoxy/internal/auth"
	"repoxy/internal/cache"
	"repoxy/internal/config"
	"repoxy/internal/janitor"
	"repoxy/internal/proxy"
	"repoxy/internal/storage"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	configPath = flag.String("config", "/etc/repoxy.yaml", "Path to configuration file")
	version    = "dev"
)

func main() {
	flag.Parse()

	log.Printf("Repoxy v%s starting...", version)

	// Load configuration
	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	// Initialize cache store
	store, err := cache.NewStore(cfg.Cache.Dir, cfg.Cache.LockTimeout)
	if err != nil {
		log.Fatalf("Failed to initialize cache store: %v", err)
	}

	// Initialize index
	indexPath := filepath.Join(cfg.Cache.Dir, "index.db")
	index, err := storage.NewIndex(indexPath)
	if err != nil {
		log.Fatalf("Failed to initialize index: %v", err)
	}

	// Check if index is empty and rebuild if needed
	count, err := index.Count()
	if err != nil {
		log.Printf("Warning: failed to count index entries: %v", err)
	} else if count == 0 {
		log.Println("Index is empty, checking for existing cached files...")
		if err := index.RebuildFromDisk(cfg.Cache.Dir); err != nil {
			log.Printf("Warning: index rebuild failed: %v", err)
		}
	} else {
		log.Printf("Index loaded: %d entries", count)
	}

	// Start janitor
	jan := janitor.New(store, index, cfg.Cache.MaxSizeBytes, 5*time.Minute)
	jan.Start()

	// Initialize handlers
	proxyHandler := proxy.New(cfg, store, index)
	adminHandler := admin.New(cfg, store, index)

	// Setup router
	r := chi.NewRouter()

	// Middleware
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(10 * time.Minute))

	// Authentication middleware (if enabled)
	if cfg.Auth.Enabled {
		r.Use(func(next http.Handler) http.Handler {
			return auth.Middleware(&cfg.Auth, next)
		})
		log.Printf("Ingress authentication enabled: %s", cfg.Auth.Type)
	}

	// Admin endpoints
	r.Get("/_healthz", adminHandler.Health)
	r.Get("/_stats", adminHandler.Stats)
	r.Handle("/_metrics", promhttp.Handler())

	if cfg.Admin.EnablePurgeAPI {
		r.Post("/_purge/by-url", adminHandler.PurgeByURL)
		r.Post("/_purge/by-regex", adminHandler.PurgeByRegex)
	}

	// Proxy handler (catch-all)
	r.HandleFunc("/*", proxyHandler.ServeHTTP)

	// Create servers for each listener
	var servers []*http.Server
	for _, listener := range cfg.Server.Listeners {
		srv := &http.Server{
			Addr:         listener.Addr,
			Handler:      r,
			ReadTimeout:  30 * time.Second,
			WriteTimeout: 10 * time.Minute,
			IdleTimeout:  120 * time.Second,
		}
		servers = append(servers, srv)

		// Start server in goroutine
		go func(s *http.Server, l config.ListenerConfig) {
			var tlsInfo string
			if l.TLS != nil {
				tlsInfo = " (TLS)"
			}
			log.Printf("Server listening on %s%s", s.Addr, tlsInfo)

			var err error
			if l.TLS != nil {
				err = s.ListenAndServeTLS(l.TLS.CertFile, l.TLS.KeyFile)
			} else {
				err = s.ListenAndServe()
			}

			if err != nil && err != http.ErrServerClosed {
				log.Fatalf("Server error on %s: %v", s.Addr, err)
			}
		}(srv, listener)
	}

	// Log configuration summary
	log.Printf("Cache directory: %s", cfg.Cache.Dir)
	log.Printf("Max cache size: %d bytes", cfg.Cache.MaxSizeBytes)
	log.Printf("Configured upstreams: %d", len(cfg.Upstreams))
	log.Printf("Configured policies: %d", len(cfg.Policies))

	// Wait for interrupt
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	<-sigCh

	log.Println("Shutting down gracefully...")

	// Shutdown all servers with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	for _, srv := range servers {
		if err := srv.Shutdown(ctx); err != nil {
			log.Printf("Server shutdown error on %s: %v", srv.Addr, err)
		}
	}

	// Stop janitor
	log.Println("Stopping janitor...")
	jan.Stop()

	// Close index database
	log.Println("Closing index...")
	if err := index.Close(); err != nil {
		log.Printf("Error closing index: %v", err)
	}

	log.Println("Server stopped")
}

func init() {
	// Custom usage message
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Repoxy v%s - Repository Proxy Cache\n\n", version)
		fmt.Fprintf(os.Stderr, "Usage: %s [options]\n\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "Options:\n")
		flag.PrintDefaults()
	}
}
