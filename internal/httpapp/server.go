package httpapp

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"html/template"
	"net"
	"net/http"
	"time"

	"dogfood/web"
)

// Config configures the HTTP server.
type Config struct {
	Port int
	DB   *sql.DB
}

// Server encapsulates the HTTP server and routing.
type Server struct {
	httpServer *http.Server
	listener   net.Listener
	db         *sql.DB
	tmpl       *template.Template
}

// NewServer builds and initializes the application router and HTTP server.
func NewServer(cfg Config) (*Server, error) {
	if cfg.Port <= 0 {
		cfg.Port = 8080
	}

	tmplFS, err := web.TemplatesFS()
	if err != nil {
		return nil, fmt.Errorf("failed to get templates fs: %w", err)
	}

	tmpl, err := template.ParseFS(tmplFS, "*.html")
	if err != nil {
		return nil, fmt.Errorf("failed to parse templates: %w", err)
	}

	staticFS, err := web.StaticFS()
	if err != nil {
		return nil, fmt.Errorf("failed to get static fs: %w", err)
	}

	mux := http.NewServeMux()

	s := &Server{
		db:   cfg.DB,
		tmpl: tmpl,
	}

	// Health check endpoint
	mux.HandleFunc("GET /healthz", s.handleHealthz)

	// Static files
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticFS))))

	// Root page
	mux.HandleFunc("GET /{$}", s.handleIndex)

	addr := fmt.Sprintf(":%d", cfg.Port)
	s.httpServer = &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	return s, nil
}

// Handler returns the underlying http.Handler for testing.
func (s *Server) Handler() http.Handler {
	return s.httpServer.Handler
}

// Start listens and serves traffic.
func (s *Server) Start() error {
	var err error
	s.listener, err = net.Listen("tcp", s.httpServer.Addr)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", s.httpServer.Addr, err)
	}
	return s.httpServer.Serve(s.listener)
}

// Addr returns the network address the server is listening on.
func (s *Server) Addr() string {
	if s.listener != nil {
		return s.listener.Addr().String()
	}
	return s.httpServer.Addr
}

// Shutdown gracefully shuts down the server.
func (s *Server) Shutdown(ctx context.Context) error {
	return s.httpServer.Shutdown(ctx)
}

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	if s.db != nil {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := s.db.PingContext(ctx); err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			_ = json.NewEncoder(w).Encode(map[string]string{
				"status": "unhealthy",
				"error":  err.Error(),
			})
			return
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"status": "ok",
	})
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.ExecuteTemplate(w, "index.html", nil); err != nil {
		http.Error(w, "internal server error", http.StatusInternalServerError)
	}
}
