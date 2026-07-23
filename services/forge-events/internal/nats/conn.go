package natsx

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/nats-io/nats.go"
)

// Metrics tracks connection and stream bootstrap counters for observability.
type Metrics struct {
	Ready      atomic.Int64
	Reconnects atomic.Uint64
	Streams    atomic.Int64
}

// Conn wraps a NATS connection with JetStream and reconnect-aware bootstrap.
type Conn struct {
	url     string
	streams []string
	log     *slog.Logger
	metrics *Metrics

	mu        sync.Mutex
	nc        *nats.Conn
	js        nats.JetStreamContext
	bootOk    atomic.Bool
	bootErr   atomic.Value // string
	closeOnce sync.Once
}

// NewConn prepares a connection manager; call Connect to dial.
func NewConn(url string, streams []string, log *slog.Logger, metrics *Metrics) *Conn {
	if log == nil {
		log = slog.Default()
	}
	if metrics == nil {
		metrics = &Metrics{}
	}
	c := &Conn{
		url:     url,
		streams: append([]string(nil), streams...),
		log:     log,
		metrics: metrics,
	}
	c.bootErr.Store("")
	c.metrics.Streams.Store(int64(len(streams)))
	return c
}

// Connect dials NATS with retry-on-failed-connect and registers reconnect handlers.
func (c *Conn) Connect(_ context.Context) error {
	c.mu.Lock()
	if c.nc != nil {
		c.mu.Unlock()
		return nil
	}
	c.mu.Unlock()

	nc, err := nats.Connect(c.url,
		nats.Name("forge-events"),
		nats.RetryOnFailedConnect(true),
		nats.MaxReconnects(-1),
		nats.ReconnectWait(2*time.Second),
		nats.DisconnectErrHandler(func(_ *nats.Conn, discErr error) {
			c.bootOk.Store(false)
			c.metrics.Ready.Store(0)
			if discErr != nil {
				c.log.Warn("nats disconnected", "error", discErr.Error())
			} else {
				c.log.Warn("nats disconnected")
			}
		}),
		nats.ReconnectHandler(func(conn *nats.Conn) {
			n := c.metrics.Reconnects.Add(1)
			c.log.Info("nats reconnected", "url", conn.ConnectedUrl(), "reconnects", n)
			c.runBootstrap("reconnect")
		}),
		nats.ClosedHandler(func(_ *nats.Conn) {
			c.bootOk.Store(false)
			c.metrics.Ready.Store(0)
			c.log.Info("nats connection closed")
		}),
		nats.ConnectHandler(func(conn *nats.Conn) {
			c.log.Info("nats connected", "url", conn.ConnectedUrl())
			c.runBootstrap("connect")
		}),
	)
	if err != nil {
		return fmt.Errorf("nats connect: %w", err)
	}

	js, err := nc.JetStream()
	if err != nil {
		nc.Close()
		return fmt.Errorf("jetstream: %w", err)
	}

	c.mu.Lock()
	c.nc = nc
	c.js = js
	c.mu.Unlock()

	// ConnectHandler may have run before js was assigned; bootstrap now if already up.
	if nc.IsConnected() {
		c.runBootstrap("startup")
	}
	return nil
}

func (c *Conn) runBootstrap(reason string) {
	c.mu.Lock()
	js := c.js
	streams := append([]string(nil), c.streams...)
	c.mu.Unlock()
	if js == nil {
		return
	}

	c.log.Info("events.bootstrap starting", "span", "events.bootstrap", "reason", reason)
	start := time.Now()
	err := BootstrapStreams(js, SpecsForNames(streams), c.log)
	if err != nil {
		c.bootOk.Store(false)
		c.metrics.Ready.Store(0)
		c.bootErr.Store(err.Error())
		c.log.Error("events.bootstrap failed",
			"span", "events.bootstrap",
			"reason", reason,
			"error", err.Error(),
			"duration_ms", time.Since(start).Milliseconds(),
		)
		return
	}
	c.bootOk.Store(true)
	c.bootErr.Store("")
	c.metrics.Ready.Store(1)
	c.log.Info("events.bootstrap complete",
		"span", "events.bootstrap",
		"reason", reason,
		"streams", len(streams),
		"duration_ms", time.Since(start).Milliseconds(),
	)
}

// IsConnected reports whether the underlying NATS client is currently connected.
func (c *Conn) IsConnected() bool {
	c.mu.Lock()
	nc := c.nc
	c.mu.Unlock()
	return nc != nil && nc.IsConnected()
}

// JetStream returns the JetStream context, or nil if not connected.
func (c *Conn) JetStream() nats.JetStreamContext {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.js
}

// ReadyError returns nil when JetStream is connected and platform streams exist.
func (c *Conn) ReadyError() error {
	if !c.IsConnected() {
		return fmt.Errorf("nats not connected")
	}
	if !c.bootOk.Load() {
		if msg, _ := c.bootErr.Load().(string); msg != "" {
			return fmt.Errorf("streams not ready: %s", msg)
		}
		return fmt.Errorf("streams not ready")
	}
	c.mu.Lock()
	js := c.js
	streams := append([]string(nil), c.streams...)
	c.mu.Unlock()
	if err := StreamsPresent(js, streams); err != nil {
		return err
	}
	return nil
}

// Metrics returns the shared metrics handle.
func (c *Conn) Metrics() *Metrics {
	return c.metrics
}

// Drain closes the connection after draining in-flight messages.
func (c *Conn) Drain() error {
	var err error
	c.closeOnce.Do(func() {
		c.mu.Lock()
		nc := c.nc
		c.mu.Unlock()
		if nc == nil {
			return
		}
		c.bootOk.Store(false)
		c.metrics.Ready.Store(0)
		err = nc.Drain()
	})
	return err
}

// Close forcefully closes the connection.
func (c *Conn) Close() {
	c.closeOnce.Do(func() {
		c.mu.Lock()
		nc := c.nc
		c.mu.Unlock()
		if nc == nil {
			return
		}
		c.bootOk.Store(false)
		c.metrics.Ready.Store(0)
		nc.Close()
	})
}
