package internal

import (
	"context"
	"time"
)

// DSNVar Environment variable in which the DSN is stored
const DSNVar = "ALERTSNITCH_BACKEND_ENDPOINT"

// Storer persists alert groups into a backend.
//
// Save receives the parsed alert group plus extraLabels — labels derived from
// the request (e.g. webhook query parameters) that a backend may attach to the
// stored record. Backends that have no notion of dynamic labels (the SQL
// backends) ignore extraLabels.
//
// Close releases backend resources within the given context. Backends that
// buffer writes (e.g. Loki batch mode) must flush within ctx before returning.
type Storer interface {
	Save(ctx context.Context, data *AlertGroup, extraLabels map[string]string) error
	Close(ctx context.Context) error
}

// Health describes the operational state of a backend. It is reported by the
// backend and acted upon by the HTTP probes — backends never touch metrics or
// HTTP concerns themselves.
type Health struct {
	// Ready reports whether the backend is reachable / live.
	Ready bool
	// Healthy reports whether the backend's schema/model is compatible.
	// Backends without a schema concept (Loki, null) always report true.
	Healthy bool
	// Detail carries a human-readable reason when Ready or Healthy is false.
	Detail string
}

// HealthChecker is implemented by backends that can report their health.
// The server type-asserts this; a backend that does not implement it is
// treated as always ready and healthy.
//
// The two checks map to the Kubernetes probe semantics:
//   - CheckLiveness answers "can I reach my backend?" — cheap, no schema work.
//     A failure means the process should be restarted.
//   - CheckReadiness answers "am I ready to serve correctly?" — reachability
//     plus schema/model compatibility. A failure means pull from rotation.
type HealthChecker interface {
	CheckLiveness(ctx context.Context) Health
	CheckReadiness(ctx context.Context) Health
}

// AlertGroup is the data read from a webhook call
type AlertGroup struct {
	Version  string `json:"version"`
	GroupKey string `json:"groupKey"`

	Receiver string `json:"receiver"`
	Status   string `json:"status"`
	Alerts   Alerts `json:"alerts"`

	GroupLabels       map[string]string `json:"groupLabels"`
	CommonLabels      map[string]string `json:"commonLabels"`
	CommonAnnotations map[string]string `json:"commonAnnotations"`

	ExternalURL string `json:"externalURL"`
}

// Alerts is a slice of Alert
type Alerts []Alert

// Alert holds one alert for notification templates.
type Alert struct {
	Status       string            `json:"status"`
	Labels       map[string]string `json:"labels"`
	Annotations  map[string]string `json:"annotations"`
	StartsAt     time.Time         `json:"startsAt"`
	EndsAt       time.Time         `json:"endsAt"`
	GeneratorURL string            `json:"generatorURL"`
	Fingerprint  string            `json:"fingerprint"`
}
