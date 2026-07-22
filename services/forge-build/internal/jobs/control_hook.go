package jobs

import (
	"context"
	"fmt"
	"strings"
	"time"

	"forge.local/services/forge-build/internal/control"
)

// ControlClient is the Control surface used after a successful build.
type ControlClient interface {
	Enabled() bool
	RecordImage(ctx context.Context, serviceID string, req control.RecordImageRequest) (control.RecordImageResponse, error)
	CreateDeployment(ctx context.Context, serviceID, buildID string, req control.CreateDeploymentRequest) (control.DeploymentResponse, error)
}

func (m *Manager) recordWithControl(parent context.Context, rec *Record) {
	if m.control == nil || !m.control.Enabled() {
		return
	}
	m.mu.RLock()
	serviceID := strings.TrimSpace(rec.ServiceID)
	image := rec.Image
	digest := rec.Digest
	commit := rec.Commit
	autoDeploy := rec.AutoDeploy
	envID := strings.TrimSpace(rec.EnvironmentID)
	already := rec.ImageRecorded
	m.mu.RUnlock()

	if serviceID == "" || image == "" || already {
		return
	}

	rec.Logs.Append(fmt.Sprintf("==> recording image on control service %s", serviceID))
	m.log.Info("control record image start",
		"build_id", rec.ID,
		"service_id", serviceID,
		"image", image,
	)

	err := m.withControlRetry(parent, func(ctx context.Context) error {
		_, err := m.control.RecordImage(ctx, serviceID, control.RecordImageRequest{
			Image:   image,
			Digest:  digest,
			Commit:  commit,
			BuildID: rec.ID,
		})
		return err
	})
	if err != nil {
		m.mu.Lock()
		rec.ImageRecorded = false
		rec.RecordedImage = ""
		rec.ControlError = "record image: " + sanitizeError(err.Error())
		m.mu.Unlock()
		_ = m.persist(rec)
		rec.Logs.Append("==> control image recording failed: " + sanitizeError(err.Error()))
		m.log.Warn("control record image failed",
			"build_id", rec.ID,
			"service_id", serviceID,
			"error", err.Error(),
		)
		return
	}

	m.mu.Lock()
	rec.ImageRecorded = true
	rec.RecordedImage = image
	rec.ControlError = ""
	m.mu.Unlock()
	_ = m.persist(rec)
	rec.Logs.Append(fmt.Sprintf("==> recorded image %s on service %s", image, serviceID))
	m.log.Info("control record image ok",
		"build_id", rec.ID,
		"service_id", serviceID,
		"image", image,
	)

	if !autoDeploy {
		return
	}
	if envID == "" {
		m.mu.Lock()
		rec.ControlError = "auto-deploy skipped: environmentId is required"
		m.mu.Unlock()
		_ = m.persist(rec)
		rec.Logs.Append("==> auto-deploy skipped: environmentId is required")
		return
	}

	rec.Logs.Append(fmt.Sprintf("==> creating deployment on control (env=%s)", envID))
	var dep control.DeploymentResponse
	err = m.withControlRetry(parent, func(ctx context.Context) error {
		var createErr error
		dep, createErr = m.control.CreateDeployment(ctx, serviceID, rec.ID, control.CreateDeploymentRequest{
			Image:         image,
			EnvironmentID: envID,
		})
		return createErr
	})
	if err != nil {
		m.mu.Lock()
		rec.ControlError = "auto-deploy: " + sanitizeError(err.Error())
		m.mu.Unlock()
		_ = m.persist(rec)
		rec.Logs.Append("==> auto-deploy failed: " + sanitizeError(err.Error()))
		m.log.Warn("control auto-deploy failed",
			"build_id", rec.ID,
			"service_id", serviceID,
			"error", err.Error(),
		)
		return
	}

	m.mu.Lock()
	rec.LinkedDeploymentID = dep.ID
	rec.ControlError = ""
	m.mu.Unlock()
	_ = m.persist(rec)
	rec.Logs.Append(fmt.Sprintf("==> linked deployment %s", dep.ID))
	m.log.Info("control auto-deploy ok",
		"build_id", rec.ID,
		"service_id", serviceID,
		"deployment_id", dep.ID,
	)
}

func (m *Manager) withControlRetry(parent context.Context, fn func(context.Context) error) error {
	attempts := m.cfg.ControlRetries
	if attempts < 1 {
		attempts = 1
	}
	backoff := m.cfg.ControlRetryBackoff
	if backoff <= 0 {
		backoff = 200 * time.Millisecond
	}
	var last error
	for i := 0; i < attempts; i++ {
		if parent.Err() != nil {
			return parent.Err()
		}
		ctx, cancel := context.WithTimeout(parent, m.cfg.ControlTimeout)
		err := fn(ctx)
		cancel()
		if err == nil {
			return nil
		}
		last = err
		if !control.Transient(err) || i == attempts-1 {
			return err
		}
		m.log.Warn("control call transient failure; retrying",
			"attempt", i+1,
			"error", err.Error(),
			"backoff_ms", backoff.Milliseconds(),
		)
		select {
		case <-parent.Done():
			return parent.Err()
		case <-time.After(backoff):
		}
		backoff *= 2
	}
	return last
}

func (m *Manager) retryPendingControlLinks() {
	if m.control == nil || !m.control.Enabled() {
		return
	}
	m.mu.RLock()
	pending := make([]*Record, 0)
	for _, rec := range m.records {
		if rec.Status == StatusSucceeded &&
			strings.TrimSpace(rec.ServiceID) != "" &&
			!rec.ImageRecorded &&
			strings.TrimSpace(rec.Image) != "" {
			pending = append(pending, rec)
		}
	}
	m.mu.RUnlock()
	for _, rec := range pending {
		m.log.Info("retrying pending control image record", "build_id", rec.ID, "service_id", rec.ServiceID)
		m.recordWithControl(m.ctx, rec)
	}
}
