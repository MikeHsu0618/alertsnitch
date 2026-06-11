package loki

import (
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/mikehsu0618/alertsnitch/internal"
)

// payload is the body of a Loki push request.
type payload struct {
	Streams []stream `json:"streams"`
}

// stream is a single Loki stream: a set of labels and its log entries.
type stream struct {
	Stream map[string]string `json:"stream"`
	Values []row             `json:"values"`
}

// row is a single Loki log entry. It marshals to Loki's ["<unix-nanos>", "<line>"]
// tuple form.
type row struct {
	At  time.Time
	Val string
}

func (r row) MarshalJSON() ([]byte, error) {
	return json.Marshal([]string{strconv.FormatInt(r.At.UnixNano(), 10), r.Val})
}

func (r *row) UnmarshalJSON(data []byte) error {
	var arr []string
	if err := json.Unmarshal(data, &arr); err != nil {
		return err
	}
	if len(arr) != 2 {
		return fmt.Errorf("expected array of length 2, got %d", len(arr))
	}

	timestamp, err := strconv.ParseInt(arr[0], 10, 64)
	if err != nil {
		return fmt.Errorf("failed to parse timestamp: %w", err)
	}

	r.At = time.Unix(0, timestamp)
	r.Val = arr[1]
	return nil
}

// FlattenAlertGroup is one alert denormalized with its group context. It is the
// JSON shape written as a single Loki log line, so each alert is independently
// queryable.
type FlattenAlertGroup struct {
	Version  string `json:"version"`
	GroupKey string `json:"groupKey"`

	Receiver string         `json:"receiver"`
	Status   string         `json:"status"`
	Alert    internal.Alert `json:"alert"`

	GroupLabels       map[string]string `json:"groupLabels"`
	CommonLabels      map[string]string `json:"commonLabels"`
	CommonAnnotations map[string]string `json:"commonAnnotations"`

	ExternalURL string `json:"externalURL"`
}
