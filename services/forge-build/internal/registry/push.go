package registry

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"forge.local/services/forge-build/internal/logbuf"
)

// ImageEngine is the Docker Engine surface needed for tag + push.
type ImageEngine interface {
	TagImage(ctx context.Context, sourceRef, targetRef string) error
	PushImage(ctx context.Context, ref string, onLine func(string)) (digest string, err error)
	ImageDigest(ctx context.Context, ref string) (string, error)
}

// Publisher tags a local image and pushes it to the configured registry.
type Publisher interface {
	TagAndPush(ctx context.Context, localImage string, refs Refs, logs *logbuf.Buffer) (digest string, err error)
}

// Client performs docker tag + push with retries and digest verification.
type Client struct {
	engine  ImageEngine
	retries int
	log     *slog.Logger
	// backoff between push attempts; zero uses a small default.
	backoff time.Duration
}

// New returns a registry publisher backed by engine.
func New(engine ImageEngine, retries int, log *slog.Logger) *Client {
	if retries < 0 {
		retries = 0
	}
	if log == nil {
		log = slog.Default()
	}
	return &Client{
		engine:  engine,
		retries: retries,
		log:     log,
		backoff: 500 * time.Millisecond,
	}
}

// SetBackoffForTest overrides the retry backoff (tests only).
func SetBackoffForTest(c *Client, d time.Duration) {
	if c != nil {
		c.backoff = d
	}
}

// TagAndPush tags localImage to the versioned (and optional latest) refs, pushes them,
// and returns the content digest of the versioned push.
func (c *Client) TagAndPush(ctx context.Context, localImage string, refs Refs, logs *logbuf.Buffer) (string, error) {
	if c == nil || c.engine == nil {
		return "", fmt.Errorf("registry client is not configured")
	}
	localImage = strings.TrimSpace(localImage)
	if localImage == "" {
		return "", fmt.Errorf("local image is required")
	}
	if strings.TrimSpace(refs.Versioned) == "" {
		return "", fmt.Errorf("versioned image ref is required")
	}

	targets := []string{refs.Versioned}
	if refs.Latest != "" {
		targets = append(targets, refs.Latest)
	}

	logsAppend(logs, fmt.Sprintf("==> tagging %s → %s", localImage, strings.Join(targets, ", ")))
	c.log.Info("registry tag", "local_image", localImage, "refs", targets)

	for _, target := range targets {
		if err := c.engine.TagImage(ctx, localImage, target); err != nil {
			logsAppend(logs, "==> docker tag failed: "+err.Error())
			return "", fmt.Errorf("docker tag %s: %w", target, err)
		}
		logsAppend(logs, "==> tagged "+target)
	}

	var digest string
	for _, target := range targets {
		d, err := c.pushWithRetries(ctx, target, logs)
		if err != nil {
			return "", err
		}
		if target == refs.Versioned {
			digest = d
		}
	}
	if digest == "" {
		d, err := c.engine.ImageDigest(ctx, refs.Versioned)
		if err != nil {
			return "", fmt.Errorf("verify digest: %w", err)
		}
		digest = d
	}
	logsAppend(logs, fmt.Sprintf("==> pushed %s digest=%s", refs.Versioned, digest))
	c.log.Info("registry push complete", "image", refs.Versioned, "digest", digest)
	return digest, nil
}

func (c *Client) pushWithRetries(ctx context.Context, ref string, logs *logbuf.Buffer) (string, error) {
	attempts := c.retries + 1
	if attempts < 1 {
		attempts = 1
	}
	var lastErr error
	for i := 1; i <= attempts; i++ {
		if i > 1 {
			logsAppend(logs, fmt.Sprintf("==> push retry %d/%d for %s", i-1, c.retries, ref))
			c.log.Warn("registry push retry", "image", ref, "attempt", i, "max_attempts", attempts, "error", lastErr.Error())
			if c.backoff > 0 {
				select {
				case <-ctx.Done():
					return "", fmt.Errorf("push canceled: %w", ctx.Err())
				case <-time.After(c.backoff):
				}
			}
		}
		logsAppend(logs, "==> docker push "+ref)
		c.log.Info("registry push start", "image", ref, "attempt", i)
		digest, err := c.engine.PushImage(ctx, ref, func(line string) {
			line = strings.TrimRight(line, "\r\n")
			if line != "" {
				logsAppend(logs, line)
			}
		})
		if err == nil {
			c.log.Info("registry push finish", "image", ref, "digest", digest, "attempt", i)
			return digest, nil
		}
		lastErr = err
		logsAppend(logs, fmt.Sprintf("==> push failed (attempt %d/%d): %s", i, attempts, err.Error()))
	}
	return "", fmt.Errorf("docker push %s failed after %d attempts: %w", ref, attempts, lastErr)
}

func logsAppend(logs *logbuf.Buffer, line string) {
	if logs != nil {
		logs.Append(line)
	}
}

// StubPublisher is a no-op publisher for unit tests that do not talk to Docker/registry.
type StubPublisher struct {
	Digest string
	Err    error
	Calls  int
}

// TagAndPush records a call and returns Digest or Err.
func (s *StubPublisher) TagAndPush(_ context.Context, _ string, refs Refs, logs *logbuf.Buffer) (string, error) {
	s.Calls++
	logsAppend(logs, "==> stub push "+refs.Versioned)
	if s.Err != nil {
		return "", s.Err
	}
	digest := s.Digest
	if digest == "" {
		digest = "sha256:stub"
	}
	logsAppend(logs, "==> stub digest "+digest)
	return digest, nil
}
