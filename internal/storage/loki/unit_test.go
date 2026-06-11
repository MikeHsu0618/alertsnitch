package loki

import (
	"encoding/json"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mikehsu0618/alertsnitch/internal"
)

func mustURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	require.NoError(t, err)
	return u
}

func TestConfig_Validate(t *testing.T) {
	tests := []struct {
		name    string
		cfg     Config
		wantErr bool
	}{
		{"nil url", Config{}, true},
		{"bad scheme", Config{URL: mustURL(t, "ftp://loki")}, true},
		{"ok http", Config{URL: mustURL(t, "http://loki:3100")}, false},
		{"timeout too short", Config{URL: mustURL(t, "http://loki"), RequestTimeout: time.Millisecond}, true},
		{"timeout too long", Config{URL: mustURL(t, "http://loki"), RequestTimeout: time.Hour}, true},
		{"basic auth missing password", Config{URL: mustURL(t, "http://loki"), Auth: AuthConfig{BasicAuthUser: "u"}}, true},
		{"basic auth missing user", Config{URL: mustURL(t, "http://loki"), Auth: AuthConfig{BasicAuthPassword: "p"}}, true},
		{"client cert without key", Config{URL: mustURL(t, "http://loki"), TLS: TLSConfig{ClientCertPath: "/x"}}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := tt.cfg
			err := cfg.Validate()
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestConfig_ValidateAppliesDefaultTimeout(t *testing.T) {
	cfg := Config{URL: mustURL(t, "http://loki:3100")}
	require.NoError(t, cfg.Validate())
	assert.Equal(t, defaultTimeout, cfg.RequestTimeout)
}

func TestStreamKey_DeterministicAndOrderIndependent(t *testing.T) {
	a := map[string]string{"b": "2", "a": "1", "c": "3"}
	b := map[string]string{"c": "3", "a": "1", "b": "2"}
	assert.Equal(t, streamKey(a), streamKey(b))
	assert.Equal(t, "{a:1,b:2,c:3}", streamKey(a))
	assert.Equal(t, "{}", streamKey(nil))
}

func TestRowJSON_RoundTrip(t *testing.T) {
	original := row{At: time.Unix(0, 1234567890), Val: "hello"}
	data, err := json.Marshal(original)
	require.NoError(t, err)
	assert.JSONEq(t, `["1234567890","hello"]`, string(data))

	var decoded row
	require.NoError(t, json.Unmarshal(data, &decoded))
	assert.Equal(t, original.Val, decoded.Val)
	assert.Equal(t, original.At.UnixNano(), decoded.At.UnixNano())
}

func TestRowUnmarshal_RejectsWrongLength(t *testing.T) {
	var r row
	assert.Error(t, json.Unmarshal([]byte(`["only-one"]`), &r))
}

func testClient(allowed ...string) *Client {
	return &Client{allowedLabels: allowedLabelSet(allowed)}
}

func TestDataToStream_GroupsByStatusAndUsesAlertTimestamps(t *testing.T) {
	start := time.Date(2025, 1, 1, 10, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)

	ag := &internal.AlertGroup{
		Version:      "4",
		Receiver:     "r",
		Status:       "firing",
		CommonLabels: map[string]string{"alertname": "X"},
		Alerts: internal.Alerts{
			{Status: "firing", StartsAt: start},
			{Status: "resolved", StartsAt: start, EndsAt: end},
		},
	}

	streams, err := testClient().dataToStream(ag, nil)
	require.NoError(t, err)
	require.Len(t, streams, 2, "one stream per status")

	byStatus := map[string]stream{}
	for _, s := range streams {
		byStatus[s.Stream["alert_status"]] = s
	}

	require.Contains(t, byStatus, "firing")
	require.Contains(t, byStatus, "resolved")
	assert.Equal(t, start.UnixNano(), byStatus["firing"].Values[0].At.UnixNano(), "firing uses StartsAt")
	assert.Equal(t, end.UnixNano(), byStatus["resolved"].Values[0].At.UnixNano(), "resolved uses EndsAt")
}

func TestDataToStream_EmptyAlertsIsError(t *testing.T) {
	_, err := testClient().dataToStream(&internal.AlertGroup{}, nil)
	assert.Error(t, err)
}

func TestBuildStreamLabels_AllowList(t *testing.T) {
	ag := &internal.AlertGroup{
		Receiver:     "r",
		Status:       "firing",
		CommonLabels: map[string]string{"alertname": "X", "severity": "warning", "secret": "nope"},
	}
	labels := testClient("severity").buildStreamLabels(ag, map[string]string{"extra": "yes"})

	assert.Equal(t, "warning", labels["severity"], "configured label promoted")
	assert.Equal(t, "yes", labels["extra"], "extra label applied")
	assert.Equal(t, "alertsnitch", labels["service_name"])
	assert.Equal(t, "X", labels["alert_name"])
	_, hasSecret := labels["secret"]
	assert.False(t, hasSecret, "non-allowed label excluded")
}
