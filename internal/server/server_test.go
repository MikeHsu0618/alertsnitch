package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/mikehsu0618/alertsnitch/internal"
)

// fakeStorer is a controllable test double for internal.Storer +
// internal.HealthChecker. It records the last AlertGroup it was asked to save
// and lets each method's behavior be injected.
type fakeStorer struct {
	saved       *internal.AlertGroup
	savedLabels map[string]string
	saveErr     error
	notReady    bool
	notHealthy  bool
	saveCalled  int
}

func (f *fakeStorer) Save(_ context.Context, data *internal.AlertGroup, extraLabels map[string]string) error {
	f.saveCalled++
	f.saved = data
	f.savedLabels = extraLabels
	return f.saveErr
}

func (f *fakeStorer) Close(context.Context) error { return nil }

func (f *fakeStorer) CheckHealth(context.Context) internal.Health {
	return internal.Health{Ready: !f.notReady, Healthy: !f.notHealthy}
}

func validPayload(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile("../webhook/sample-payload.json")
	assert.NoError(t, err)
	return b
}

func post(s *Server, path string, body []byte) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(string(body)))
	rec := httptest.NewRecorder()
	s.r.ServeHTTP(rec, req)
	return rec
}

func TestWebhookPost_ValidPayloadIsSaved(t *testing.T) {
	fake := &fakeStorer{}
	s := New(fake, false)

	rec := post(s, "/webhook", validPayload(t))

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, 1, fake.saveCalled, "Save should be called exactly once")
	assert.NotNil(t, fake.saved)
}

func TestWebhookPost_InvalidJSONReturns400(t *testing.T) {
	fake := &fakeStorer{}
	s := New(fake, false)

	rec := post(s, "/webhook", []byte("{not json"))

	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Equal(t, 0, fake.saveCalled, "Save must not be called for an invalid payload")
}

func TestWebhookPost_UnsupportedVersionReturns400(t *testing.T) {
	fake := &fakeStorer{}
	s := New(fake, false)

	rec := post(s, "/webhook", []byte(`{"version":"3","status":"firing","alerts":[]}`))

	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Equal(t, 0, fake.saveCalled)
}

func TestWebhookPost_SaveFailureReturns500(t *testing.T) {
	fake := &fakeStorer{saveErr: assert.AnError}
	s := New(fake, false)

	rec := post(s, "/webhook", validPayload(t))

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.Equal(t, 1, fake.saveCalled)
}

func TestReadyProbe(t *testing.T) {
	t.Run("ready when ping and model ok", func(t *testing.T) {
		s := New(&fakeStorer{}, false)
		req := httptest.NewRequest(http.MethodGet, "/-/ready", nil)
		rec := httptest.NewRecorder()
		s.r.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusOK, rec.Code)
	})

	t.Run("not ready when ping fails", func(t *testing.T) {
		s := New(&fakeStorer{notReady: true}, false)
		req := httptest.NewRequest(http.MethodGet, "/-/ready", nil)
		rec := httptest.NewRecorder()
		s.r.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	})

	t.Run("not ready when model invalid", func(t *testing.T) {
		s := New(&fakeStorer{notHealthy: true}, false)
		req := httptest.NewRequest(http.MethodGet, "/-/ready", nil)
		rec := httptest.NewRecorder()
		s.r.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	})
}

func TestHealthProbe(t *testing.T) {
	t.Run("healthy when ping ok", func(t *testing.T) {
		s := New(&fakeStorer{}, false)
		req := httptest.NewRequest(http.MethodGet, "/-/health", nil)
		rec := httptest.NewRecorder()
		s.r.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusOK, rec.Code)
	})

	t.Run("unhealthy when ping fails", func(t *testing.T) {
		s := New(&fakeStorer{notReady: true}, false)
		req := httptest.NewRequest(http.MethodGet, "/-/health", nil)
		rec := httptest.NewRecorder()
		s.r.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	})
}
