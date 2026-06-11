package loki

import "errors"

// errQueueFull is returned when an alert cannot be enqueued for batched
// delivery because the buffer is saturated.
var errQueueFull = errors.New("loki alert queue is full")
