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

	"github.com/mikehsu0618/alertsnitch/internal"
	"github.com/mikehsu0618/alertsnitch/internal/metrics"
	"github.com/mikehsu0618/alertsnitch/internal/webhook"
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

	s := &Server{
		db:    db,
		r:     r,
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

// queryLabels extracts the request's query parameters as a label map. These
// become extra stream labels for backends that support them (Loki). This used
// to live in a middleware that the storage layer reached into via context;
// extracting it here keeps the storage layer free of HTTP concerns.
func queryLabels(r *http.Request) map[string]string {
	q := r.URL.Query()
	labels := make(map[string]string, len(q))
	for key, values := range q {
		if len(values) > 0 {
			labels[key] = values[0]
		}
	}
	return labels
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

	// The backend owns the saved/failed counters, recording them at the real
	// point of persistence (which, for Loki batch mode, is asynchronous).
	if err = s.db.Save(r.Context(), data, queryLabels(r)); err != nil {
		logrus.Errorf("failed to save alerts: %s", err)
		http.Error(w, fmt.Sprintf("failed to save alerts: %s", err), http.StatusInternalServerError)
		return
	}
}

// healthyProbe is the liveness probe: it only checks that the backend is
// reachable (no schema query), so a frequent probe stays cheap.
func (s *Server) healthyProbe(w http.ResponseWriter, r *http.Request) {
	h := s.probe(r.Context(), internal.HealthChecker.CheckLiveness)
	if !h.Ready {
		logrus.Errorf("backend is not reachable: %s", h.Detail)
		http.Error(w, fmt.Sprintf("backend is not reachable: %s", h.Detail), http.StatusServiceUnavailable)
		return
	}
}

// readyProbe is the readiness probe: reachability plus schema/model compatibility.
func (s *Server) readyProbe(w http.ResponseWriter, r *http.Request) {
	h := s.probe(r.Context(), internal.HealthChecker.CheckReadiness)
	if !h.Ready {
		logrus.Errorf("backend is not reachable: %s", h.Detail)
		http.Error(w, fmt.Sprintf("backend is not reachable: %s", h.Detail), http.StatusServiceUnavailable)
		return
	}
	if !h.Healthy {
		logrus.Errorf("backend model is invalid: %s", h.Detail)
		http.Error(w, fmt.Sprintf("backend model is invalid: %s", h.Detail), http.StatusServiceUnavailable)
		return
	}
}

// probe runs the given health check (if the backend reports health) and reflects
// reachability into the DatabaseUp gauge. Backends that do not implement
// HealthChecker are treated as always ready and healthy.
func (s *Server) probe(ctx context.Context, check func(internal.HealthChecker, context.Context) internal.Health) internal.Health {
	checker, ok := s.db.(internal.HealthChecker)
	if !ok {
		metrics.DatabaseUp.Set(1)
		return internal.Health{Ready: true, Healthy: true}
	}

	h := check(checker, ctx)
	if h.Ready {
		metrics.DatabaseUp.Set(1)
	} else {
		metrics.DatabaseUp.Set(0)
	}
	return h
}
