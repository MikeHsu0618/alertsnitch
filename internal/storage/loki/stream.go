package loki

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/mikehsu0618/alertsnitch/internal"
)

// defaultAllowedLabels is the built-in set of alert labels promoted to Loki
// stream labels when the operator does not configure an explicit allow-list.
var defaultAllowedLabels = []string{
	"severity", "priority", "level", "instance", "job", "team", "env",
	"service", "pod", "namespace", "node", "container", "cluster",
}

// allowedLabelSet builds a lookup set from the configured labels, falling back
// to defaultAllowedLabels when none are configured.
func allowedLabelSet(configured []string) map[string]bool {
	labels := configured
	if len(labels) == 0 {
		labels = defaultAllowedLabels
	}
	set := make(map[string]bool, len(labels))
	for _, l := range labels {
		set[l] = true
	}
	return set
}

func cloneLabels(labels map[string]string) map[string]string {
	clone := make(map[string]string, len(labels))
	for k, v := range labels {
		clone[k] = v
	}
	return clone
}

func groupAlertsByStatus(alerts []internal.Alert) map[string][]internal.Alert {
	byStatus := make(map[string][]internal.Alert)
	for _, alert := range alerts {
		byStatus[alert.Status] = append(byStatus[alert.Status], alert)
	}
	return byStatus
}

// dataToStream converts an alert group into one Loki stream per alert status.
func (c *Client) dataToStream(data *internal.AlertGroup, extraLabels map[string]string) ([]stream, error) {
	if len(data.Alerts) == 0 {
		return nil, fmt.Errorf("no alerts to process")
	}

	byStatus := groupAlertsByStatus(data.Alerts)
	baseLabels := c.buildStreamLabels(data, extraLabels)

	streams := make([]stream, 0, len(byStatus))
	for status, alerts := range byStatus {
		s, err := createStreamForStatus(status, alerts, data, baseLabels)
		if err != nil {
			return nil, err
		}
		streams = append(streams, s)
	}
	return streams, nil
}

func createStreamForStatus(status string, alerts []internal.Alert, data *internal.AlertGroup, baseLabels map[string]string) (stream, error) {
	streamLabels := cloneLabels(baseLabels)
	streamLabels["alert_status"] = status

	s := stream{
		Stream: streamLabels,
		Values: make([]row, 0, len(alerts)),
	}

	for _, alert := range alerts {
		// Use the alert's real timestamp rather than time.Now() so history is
		// accurate: StartsAt for firing, EndsAt for resolved when valid.
		timestamp := alert.StartsAt
		if status == "resolved" && !alert.EndsAt.IsZero() && alert.EndsAt.After(alert.StartsAt) {
			timestamp = alert.EndsAt
		}
		if timestamp.IsZero() {
			timestamp = time.Now()
		}

		flattened := FlattenAlertGroup{
			Version:           data.Version,
			GroupKey:          data.GroupKey,
			Receiver:          data.Receiver,
			Status:            data.Status,
			Alert:             alert,
			GroupLabels:       data.GroupLabels,
			CommonLabels:      data.CommonLabels,
			CommonAnnotations: data.CommonAnnotations,
			ExternalURL:       data.ExternalURL,
		}

		jsonData, err := json.Marshal(flattened)
		if err != nil {
			return stream{}, fmt.Errorf("error marshaling FlattenAlertGroup: %w", err)
		}

		s.Values = append(s.Values, row{At: timestamp, Val: string(jsonData)})
	}

	return s, nil
}

func (c *Client) buildStreamLabels(data *internal.AlertGroup, extraLabels map[string]string) map[string]string {
	streamLabels := make(map[string]string, len(extraLabels)+len(data.CommonLabels)+len(data.GroupLabels)+4)

	for key, value := range extraLabels {
		streamLabels[key] = value
	}
	for label, value := range data.CommonLabels {
		if c.allowedLabels[label] {
			streamLabels[label] = value
		}
	}
	for label, value := range data.GroupLabels {
		if c.allowedLabels[label] {
			streamLabels[label] = value
		}
	}

	streamLabels["service_name"] = "alertsnitch"
	streamLabels["receiver"] = data.Receiver
	streamLabels["status"] = data.Status
	streamLabels["alert_name"] = data.CommonLabels["alertname"]

	return streamLabels
}

// streamKey is a deterministic string identity for a label set, used to merge
// entries belonging to the same stream during batching.
func streamKey(labels map[string]string) string {
	if len(labels) == 0 {
		return "{}"
	}

	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var buf strings.Builder
	buf.WriteByte('{')
	for i, k := range keys {
		if i > 0 {
			buf.WriteByte(',')
		}
		buf.WriteString(k)
		buf.WriteByte(':')
		buf.WriteString(labels[k])
	}
	buf.WriteByte('}')
	return buf.String()
}
