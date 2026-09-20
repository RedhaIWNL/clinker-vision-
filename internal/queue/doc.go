// Package queue provides bounded per-camera frame queues. Legacy callers can
// use Push (drop-oldest); the dense production lane uses PushWait so upstream
// decoding applies backpressure and never silently drops frames.
package queue
