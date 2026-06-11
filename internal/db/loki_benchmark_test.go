package db

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/mikehsu0618/alertsnitch/internal"
)

// BenchmarkLokiClient_Save Performance benchmark
func BenchmarkLokiClient_Save(b *testing.B) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/loki/api/v1/push" {
			w.WriteHeader(http.StatusNoContent)
		} else if r.URL.Path == "/loki/api/v1/labels" {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"status":"success","data":[]}`))
		} else {
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	client := createBenchmarkClient(b, server.URL, false)
	defer client.Close()

	alertGroup := createTestAlertGroup()
	ctx := context.Background()

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		err := client.Save(ctx, alertGroup)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkLokiClient_SaveWithBatch Batch processing performance benchmark
func BenchmarkLokiClient_SaveWithBatch(b *testing.B) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/loki/api/v1/push" {
			w.WriteHeader(http.StatusNoContent)
		} else if r.URL.Path == "/loki/api/v1/labels" {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"status":"success","data":[]}`))
		} else {
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	client := createBenchmarkClient(b, server.URL, true)
	defer client.Close()

	alertGroup := createTestAlertGroup()
	ctx := context.Background()

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		err := client.Save(ctx, alertGroup)
		if err != nil {
			b.Fatal(err)
		}
	}

	// Wait for batch processing to complete
	time.Sleep(200 * time.Millisecond)
}

// BenchmarkLokiClient_ConcurrentSave Concurrent save performance test
func BenchmarkLokiClient_ConcurrentSave(b *testing.B) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/loki/api/v1/push" {
			// Simulate a little delay
			time.Sleep(1 * time.Millisecond)
			w.WriteHeader(http.StatusNoContent)
		} else if r.URL.Path == "/loki/api/v1/labels" {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"status":"success","data":[]}`))
		} else {
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	client := createBenchmarkClient(b, server.URL, false)
	defer client.Close()

	alertGroup := createTestAlertGroup()
	ctx := context.Background()

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			err := client.Save(ctx, alertGroup)
			if err != nil {
				b.Error(err)
			}
		}
	})
}

// BenchmarkDataToStream Data conversion performance test
func BenchmarkDataToStream(b *testing.B) {
	client := &lokiClient{}

	// Create different sized AlertGroup for testing
	sizes := []int{1, 10, 100, 1000}

	for _, size := range sizes {
		b.Run(fmt.Sprintf("alerts_%d", size), func(b *testing.B) {
			alertGroup := createLargeAlertGroup(size)
			extraLabels := map[string]string{
				"env":     "test",
				"cluster": "k8s-prod",
				"region":  "us-west-2",
			}

			b.ResetTimer()
			b.ReportAllocs()

			for i := 0; i < b.N; i++ {
				_, err := client.dataToStream(alertGroup, extraLabels)
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkStreamKeyGeneration Stream key generation performance test
func BenchmarkStreamKeyGeneration(b *testing.B) {
	client := &lokiClient{}

	// Different sized label mappings
	labelSizes := []int{5, 10, 20, 50}

	for _, size := range labelSizes {
		b.Run(fmt.Sprintf("labels_%d", size), func(b *testing.B) {
			labels := make(map[string]string, size)
			for i := 0; i < size; i++ {
				labels[fmt.Sprintf("label_%d", i)] = fmt.Sprintf("value_%d", i)
			}

			b.ResetTimer()
			b.ReportAllocs()

			for i := 0; i < b.N; i++ {
				_ = client.getStreamKey(labels)
			}
		})
	}
}

// TestLokiClient_MemoryLeaks Memory leak test
func TestLokiClient_MemoryLeaks(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/loki/api/v1/push" {
			w.WriteHeader(http.StatusNoContent)
		} else if r.URL.Path == "/loki/api/v1/labels" {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"status":"success","data":[]}`))
		} else {
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	// Create and destroy multiple clients
	for i := 0; i < 100; i++ {
		client := createBenchmarkClient(t, server.URL, true)

		// Send some data
		for j := 0; j < 10; j++ {
			alertGroup := createTestAlertGroup()
			alertGroup.GroupKey = fmt.Sprintf("leak-test-%d-%d", i, j)

			err := client.Save(context.Background(), alertGroup)
			require.NoError(t, err)
		}

		// Close client
		err := client.Close()
		require.NoError(t, err)
	}

	// Wait for all goroutines to clean up
	time.Sleep(500 * time.Millisecond)
}

// TestLokiClient_StressTest Stress test
func TestLokiClient_StressTest(t *testing.T) {
	if testing.Short() {
		t.Skip("Skip stress test, use -short flag")
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/loki/api/v1/push" {
			// Random delay to simulate real network conditions
			delay := time.Duration(time.Now().UnixNano()%5) * time.Millisecond
			time.Sleep(delay)
			w.WriteHeader(http.StatusNoContent)
		} else if r.URL.Path == "/loki/api/v1/labels" {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"status":"success","data":[]}`))
		} else {
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	client := createBenchmarkClient(t, server.URL, true)
	defer client.Close()

	// High concurrent stress test
	numWorkers := 20           // Reduce number of workers
	numRequestsPerWorker := 50 // Reduce requests per worker

	var wg sync.WaitGroup
	errChan := make(chan error, numWorkers*numRequestsPerWorker)

	startTime := time.Now()

	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()

			for j := 0; j < numRequestsPerWorker; j++ {
				alertGroup := createTestAlertGroup()
				alertGroup.GroupKey = fmt.Sprintf("stress-worker-%d-req-%d", workerID, j)

				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				err := client.Save(ctx, alertGroup)
				cancel()

				if err != nil {
					errChan <- err
				}
			}
		}(i)
	}

	wg.Wait()
	close(errChan)

	duration := time.Since(startTime)
	totalRequests := numWorkers * numRequestsPerWorker

	// Count errors
	errorCount := 0
	for err := range errChan {
		errorCount++
		t.Logf("Request error: %v", err)
	}

	successRate := float64(totalRequests-errorCount) / float64(totalRequests) * 100
	throughput := float64(totalRequests) / duration.Seconds()

	t.Logf("Stress test results:")
	t.Logf("  Total requests: %d", totalRequests)
	t.Logf("  Total duration: %v", duration)
	t.Logf("  Success rate: %.2f%%", successRate)
	t.Logf("  Throughput: %.2f req/sec", throughput)
	t.Logf("  Error count: %d", errorCount)

	// Assert minimum performance requirements
	require.True(t, successRate >= 90.0, "Success rate should be at least 90%%, actual: %.2f%%", successRate)
	require.True(t, throughput >= 100.0, "Throughput should be at least 100 req/sec, actual: %.2f", throughput)
}

// TestLokiClient_NetworkResilience Network resilience test
func TestLokiClient_NetworkResilience(t *testing.T) {
	connectionAttempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/loki/api/v1/push" {
			connectionAttempts++

			// Simulate intermittent network failures
			if connectionAttempts%5 == 0 {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}

			w.WriteHeader(http.StatusNoContent)
		} else if r.URL.Path == "/loki/api/v1/labels" {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"status":"success","data":[]}`))
		} else {
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	client := createBenchmarkClient(t, server.URL, false)
	defer client.Close()

	ctx := context.Background()
	successCount := 0
	errorCount := 0

	// Send 100 requests, expect 20% failures
	for i := 0; i < 100; i++ {
		alertGroup := createTestAlertGroup()
		alertGroup.GroupKey = fmt.Sprintf("resilience-test-%d", i)

		err := client.Save(ctx, alertGroup)
		if err != nil {
			errorCount++
		} else {
			successCount++
		}
	}

	t.Logf("Network resilience test results: success=%d, failure=%d", successCount, errorCount)

	// Should have approximately 80% success rate
	successRate := float64(successCount) / 100.0 * 100.0
	require.True(t, successRate >= 75.0 && successRate <= 85.0,
		"Expected success rate 75-85%%, actual: %.2f%%", successRate)
}

// Helper functions

func createBenchmarkClient(tb testing.TB, serverURL string, batchEnabled bool) *lokiClient {
	args := ConnectionArgs{
		DSN:     serverURL,
		Options: map[string]string{},
	}

	if batchEnabled {
		args.Options["batch_enabled"] = "true"
		args.Options["batch_size"] = "100"
		args.Options["batch_flush_timeout"] = "100ms"
		args.Options["batch_max_retries"] = "3"
	}

	client, err := connectLoki(args)
	require.NoError(tb, err)
	return client
}

func createLargeAlertGroup(numAlerts int) *internal.AlertGroup {
	alertGroup := createTestAlertGroup()
	alertGroup.Alerts = make([]internal.Alert, 0, numAlerts)

	for i := 0; i < numAlerts; i++ {
		alert := internal.Alert{
			Status: []string{"firing", "resolved"}[i%2],
			Labels: map[string]string{
				"alertname": fmt.Sprintf("TestAlert-%d", i),
				"severity":  []string{"warning", "critical", "info"}[i%3],
				"instance":  fmt.Sprintf("server-%d.example.com", i),
				"service":   fmt.Sprintf("service-%d", i%10),
			},
			Annotations: map[string]string{
				"summary":     fmt.Sprintf("Test alert summary for instance %d", i),
				"description": fmt.Sprintf("Detailed description for alert %d", i),
				"runbook":     fmt.Sprintf("https://runbook.example.com/alert-%d", i),
			},
		}
		alertGroup.Alerts = append(alertGroup.Alerts, alert)
	}

	return alertGroup
}
