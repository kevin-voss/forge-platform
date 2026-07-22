package docker

import (
	"archive/tar"
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// BuildOptions configure an Engine /build request.
type BuildOptions struct {
	ContextDir string
	Dockerfile string
	Tags       []string
	OnLine     func(string)
}

type buildStreamMessage struct {
	Stream      string `json:"stream"`
	Status      string `json:"status"`
	Progress    string `json:"progress"`
	Error       string `json:"error"`
	ErrorDetail *struct {
		Message string `json:"message"`
	} `json:"errorDetail"`
	Aux json.RawMessage `json:"aux"`
}

// BuildImage runs POST /build with a tarred context directory.
func (c *Client) BuildImage(ctx context.Context, opts BuildOptions) error {
	if strings.TrimSpace(opts.ContextDir) == "" {
		return fmt.Errorf("build context dir is required")
	}
	dockerfile := strings.TrimSpace(opts.Dockerfile)
	if dockerfile == "" {
		dockerfile = "Dockerfile"
	}

	pr, pw := io.Pipe()
	errCh := make(chan error, 1)
	go func() {
		errCh <- writeContextTar(pw, opts.ContextDir)
	}()

	q := url.Values{}
	q.Set("dockerfile", filepath.ToSlash(dockerfile))
	q.Set("rm", "true")
	q.Set("forcerm", "true")
	for _, tag := range opts.Tags {
		tag = strings.TrimSpace(tag)
		if tag != "" {
			q.Add("t", tag)
		}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/build?"+q.Encode(), pr)
	if err != nil {
		_ = pr.Close()
		return fmt.Errorf("docker build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-tar")

	// Long-running build: do not apply the short Ping client timeout.
	client := &http.Client{Transport: c.httpClient.Transport}
	resp, err := client.Do(req)
	if err != nil {
		_ = pr.Close()
		<-errCh
		return fmt.Errorf("docker build: %w", err)
	}
	defer resp.Body.Close()

	if tarErr := <-errCh; tarErr != nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		return fmt.Errorf("tar build context: %w", tarErr)
	}

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return fmt.Errorf("docker build: unexpected status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	var buildErr error
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var msg buildStreamMessage
		if err := json.Unmarshal(line, &msg); err != nil {
			if opts.OnLine != nil {
				opts.OnLine(string(line))
			}
			continue
		}
		text := msg.Stream
		if text == "" && msg.Status != "" {
			text = msg.Status
			if msg.Progress != "" {
				text = text + " " + msg.Progress
			}
		}
		if text != "" && opts.OnLine != nil {
			opts.OnLine(text)
		}
		if msg.Error != "" {
			detail := msg.Error
			if msg.ErrorDetail != nil && msg.ErrorDetail.Message != "" {
				detail = msg.ErrorDetail.Message
			}
			if opts.OnLine != nil {
				opts.OnLine(detail)
			}
			buildErr = fmt.Errorf("docker build failed: %s", detail)
		}
	}
	if err := scanner.Err(); err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("docker build canceled: %w", ctx.Err())
		}
		return fmt.Errorf("docker build stream: %w", err)
	}
	if buildErr != nil {
		return buildErr
	}
	if ctx.Err() != nil {
		return fmt.Errorf("docker build canceled: %w", ctx.Err())
	}
	return nil
}

func encodeImageRef(ref string) string {
	// Preserve path separators and tag colon; escape other reserved characters.
	escaped := url.PathEscape(ref)
	escaped = strings.ReplaceAll(escaped, "%2F", "/")
	escaped = strings.ReplaceAll(escaped, "%3A", ":")
	return escaped
}

func writeContextTar(w io.WriteCloser, root string) error {
	defer w.Close()
	tw := tar.NewWriter(w)
	defer tw.Close()

	root = filepath.Clean(root)
	return filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		// Skip VCS metadata in the build context.
		base := info.Name()
		if info.IsDir() && (base == ".git" || base == ".tmp") {
			return filepath.SkipDir
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		header, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		header.Name = filepath.ToSlash(rel)
		if info.IsDir() {
			header.Name += "/"
		}
		if err := tw.WriteHeader(header); err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = io.Copy(tw, f)
		return err
	})
}

// ImageExists reports whether a local image reference is present.
func (c *Client) ImageExists(ctx context.Context, ref string) (bool, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return false, fmt.Errorf("image ref is required")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/images/"+encodeImageRef(ref)+"/json", nil)
	if err != nil {
		return false, err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	switch resp.StatusCode {
	case http.StatusOK:
		return true, nil
	case http.StatusNotFound:
		return false, nil
	default:
		return false, fmt.Errorf("docker image inspect: unexpected status %d", resp.StatusCode)
	}
}

// RemoveImage deletes a local image reference (best-effort for cleanup helpers).
func (c *Client) RemoveImage(ctx context.Context, ref string) error {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return fmt.Errorf("image ref is required")
	}
	q := url.Values{}
	q.Set("force", "true")
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, c.baseURL+"/images/"+encodeImageRef(ref)+"?"+q.Encode(), nil)
	if err != nil {
		return err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNotFound {
		return fmt.Errorf("docker image remove: unexpected status %d", resp.StatusCode)
	}
	return nil
}
