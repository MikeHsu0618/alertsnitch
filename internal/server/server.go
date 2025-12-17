package server

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/gorilla/mux"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/sirupsen/logrus"

	"gitlab.com/yakshaving.art/alertsnitch/internal"
	"gitlab.com/yakshaving.art/alertsnitch/internal/metrics"
	"gitlab.com/yakshaving.art/alertsnitch/internal/middleware"
	"gitlab.com/yakshaving.art/alertsnitch/internal/webhook"
)

// SupportedWebhookVersion is the alert webhook data version that is supported
// by this app
const SupportedWebhookVersion = "4"

// Server represents a web server that processes webhooks
type Server struct {
	db         internal.Storer
	r          *mux.Router
	httpServer *http.Server

	debug bool
}

// New returns a new web server
func New(db internal.Storer, debug bool) *Server {
	r := mux.NewRouter()

	r.Use(middleware.WithQueryParameters)

	s := &Server{
		db: db,
		r:  r,

		debug: debug,
	}

	r.HandleFunc("/webhook", s.webhookPost).Methods("POST")
	r.HandleFunc("/-/ready", s.readyProbe).Methods("GET")
	r.HandleFunc("/-/health", s.healthyProbe).Methods("GET")
	r.Handle("/metrics", promhttp.Handler())

	return s
}

// Start starts a new server on the given address
func (s *Server) Start(address string) error {
	s.httpServer = &http.Server{
		Addr:         address,
		Handler:      s.r,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	logrus.Infof("Starting listener on %s", address)
	if err := s.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("failed to start server: %w", err)
	}
	return nil
}

// Shutdown gracefully shuts down the server
func (s *Server) Shutdown(ctx context.Context) error {
	if s.httpServer == nil {
		return nil
	}

	logrus.Info("Shutting down HTTP server...")
	if err := s.httpServer.Shutdown(ctx); err != nil {
		return fmt.Errorf("failed to shutdown server: %w", err)
	}
	return nil
}

func (s *Server) webhookPost(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()

	metrics.WebhooksReceivedTotal.Inc()

	body, err := io.ReadAll(r.Body)
	if err != nil {
		metrics.InvalidWebhooksTotal.Inc()
		logrus.Errorf("Failed to read payload: %s", err)
		http.Error(w, fmt.Sprintf("Failed to read payload: %s", err), http.StatusBadRequest)
		return
	}

	if s.debug {
		logrus.Debugf("Received webhook payload: %s", string(body))
	}

	data, err := webhook.Parse(body)
	if err != nil {
		metrics.InvalidWebhooksTotal.Inc()

		logrus.Errorf("Invalid payload: %s", err)
		http.Error(w, fmt.Sprintf("Invalid payload: %s", err), http.StatusBadRequest)
		return
	}

	if data.Version != SupportedWebhookVersion {
		metrics.InvalidWebhooksTotal.Inc()

		logrus.Errorf("Invalid payload: webhook version %s is not supported", data.Version)
		http.Error(w, fmt.Sprintf("Invalid payload: webhook version %s is not supported", data.Version), http.StatusBadRequest)
		return
	}

	metrics.AlertsReceivedTotal.WithLabelValues(data.Receiver, data.Status).Add(float64(len(data.Alerts)))

	if err = s.db.Save(r.Context(), data); err != nil {
		metrics.AlertsSavingFailuresTotal.WithLabelValues(data.Receiver, data.Status).Add(float64(len(data.Alerts)))

		logrus.Errorf("failed to save alerts: %s", err)
		http.Error(w, fmt.Sprintf("failed to save alerts: %s", err), http.StatusInternalServerError)
		return
	}
	metrics.AlertsSavedTotal.WithLabelValues(data.Receiver, data.Status).Add(float64(len(data.Alerts)))
}

func (s *Server) healthyProbe(w http.ResponseWriter, r *http.Request) {
	if err := s.db.Ping(); err != nil {
		logrus.Errorf("failed to ping database server: %s", err)
		http.Error(w, fmt.Sprintf("failed to ping database server: %s", err), http.StatusServiceUnavailable)
		return
	}
}

func (s *Server) readyProbe(w http.ResponseWriter, r *http.Request) {
	if err := s.db.Ping(); err != nil {
		logrus.Errorf("database is not reachable: %s", err)
		http.Error(w, fmt.Sprintf("database is not reachable: %s", err), http.StatusServiceUnavailable)
		return
	}
	if err := s.db.CheckModel(); err != nil {
		logrus.Errorf("invalid model: %s", err)
		http.Error(w, fmt.Sprintf("invalid model: %s", err), http.StatusServiceUnavailable)
		return
	}
}
