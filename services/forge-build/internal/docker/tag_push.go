package docker

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// anonymousRegistryAuth is base64("{}") — required even for anonymous local registries.
const anonymousRegistryAuth = "e30="

type pushStreamMessage struct {
	Status      string `json:"status"`
	Progress    string `json:"progress"`
	Error       string `json:"error"`
	ErrorDetail *struct {
		Message string `json:"message"`
	} `json:"errorDetail"`
	Aux json.RawMessage `json:"aux"`
}

type pushAux struct {
	Digest string `json:"Digest"`
	Tag    string `json:"Tag"`
	Size   int64  `json:"Size"`
}

type imageInspect struct {
	Id          string   `json:"Id"`
	RepoDigests []string `json:"RepoDigests"`
}

// TagImage creates targetRef as a new tag for sourceRef (docker tag).
func (c *Client) TagImage(ctx context.Context, sourceRef, targetRef string) error {
	sourceRef = strings.TrimSpace(sourceRef)
	targetRef = strings.TrimSpace(targetRef)
	if sourceRef == "" || targetRef == "" {
		return fmt.Errorf("source and target image refs are required")
	}
	repo, tag, err := splitRepoTag(targetRef)
	if err != nil {
		return err
	}
	q := url.Values{}
	q.Set("repo", repo)
	q.Set("tag", tag)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.baseURL+"/images/"+encodeImageRef(sourceRef)+"/tag?"+q.Encode(), nil)
	if err != nil {
		return fmt.Errorf("docker tag request: %w", err)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("docker tag: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		return fmt.Errorf("docker tag: unexpected status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

// PushImage pushes ref to its registry and returns the content digest when available.
func (c *Client) PushImage(ctx context.Context, ref string, onLine func(string)) (string, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", fmt.Errorf("image ref is required")
	}
	repo, tag, err := splitRepoTag(ref)
	if err != nil {
		return "", err
	}
	q := url.Values{}
	if tag != "" {
		q.Set("tag", tag)
	}
	endpoint := c.baseURL + "/images/" + encodeImageRef(repo) + "/push"
	if enc := q.Encode(); enc != "" {
		endpoint += "?" + enc
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, nil)
	if err != nil {
		return "", fmt.Errorf("docker push request: %w", err)
	}
	req.Header.Set("X-Registry-Auth", registryAuthHeader())
	client := &http.Client{Transport: c.httpClient.Transport}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("docker push: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return "", fmt.Errorf("docker push: unexpected status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	var pushErr error
	var digest string
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var msg pushStreamMessage
		if err := json.Unmarshal(line, &msg); err != nil {
			if onLine != nil {
				onLine(string(line))
			}
			continue
		}
		text := msg.Status
		if text != "" && msg.Progress != "" {
			text = text + " " + msg.Progress
		}
		if text != "" && onLine != nil {
			onLine(text)
		}
		if len(msg.Aux) > 0 {
			var aux pushAux
			if err := json.Unmarshal(msg.Aux, &aux); err == nil && strings.TrimSpace(aux.Digest) != "" {
				digest = strings.TrimSpace(aux.Digest)
			}
		}
		if msg.Error != "" {
			detail := msg.Error
			if msg.ErrorDetail != nil && msg.ErrorDetail.Message != "" {
				detail = msg.ErrorDetail.Message
			}
			if onLine != nil {
				onLine(detail)
			}
			pushErr = fmt.Errorf("docker push failed: %s", detail)
		}
	}
	if err := scanner.Err(); err != nil {
		if ctx.Err() != nil {
			return "", fmt.Errorf("docker push canceled: %w", ctx.Err())
		}
		return "", fmt.Errorf("docker push stream: %w", err)
	}
	if pushErr != nil {
		return "", pushErr
	}
	if digest == "" {
		d, err := c.ImageDigest(ctx, ref)
		if err != nil {
			return "", fmt.Errorf("push succeeded but digest unavailable: %w", err)
		}
		digest = d
	}
	if ctx.Err() != nil {
		return "", fmt.Errorf("docker push canceled: %w", ctx.Err())
	}
	return digest, nil
}

// ImageDigest returns the sha256 digest for a local image reference.
func (c *Client) ImageDigest(ctx context.Context, ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", fmt.Errorf("image ref is required")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/images/"+encodeImageRef(ref)+"/json", nil)
	if err != nil {
		return "", err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return "", fmt.Errorf("docker image inspect read: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("docker image inspect: unexpected status %d", resp.StatusCode)
	}
	var inspect imageInspect
	if err := json.Unmarshal(body, &inspect); err != nil {
		return "", fmt.Errorf("docker image inspect decode: %w", err)
	}
	repo, _, _ := splitRepoTag(ref)
	for _, rd := range inspect.RepoDigests {
		if i := strings.IndexByte(rd, '@'); i >= 0 {
			name, dig := rd[:i], rd[i+1:]
			if name == repo || strings.HasPrefix(ref, name) {
				return dig, nil
			}
		}
	}
	if len(inspect.RepoDigests) > 0 {
		rd := inspect.RepoDigests[0]
		if i := strings.IndexByte(rd, '@'); i >= 0 {
			return rd[i+1:], nil
		}
	}
	if strings.HasPrefix(inspect.Id, "sha256:") {
		return inspect.Id, nil
	}
	return "", fmt.Errorf("no digest found for %s", ref)
}

func splitRepoTag(ref string) (repo, tag string, err error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", "", fmt.Errorf("image ref is required")
	}
	// Digest refs are not used for tag/push targets in this service.
	if strings.Contains(ref, "@") {
		return "", "", fmt.Errorf("digest image refs are not supported for tag/push: %s", ref)
	}
	// Prefer the tag after the last ':' that is not part of a host:port prefix.
	lastColon := strings.LastIndexByte(ref, ':')
	lastSlash := strings.LastIndexByte(ref, '/')
	if lastColon > lastSlash {
		repo = ref[:lastColon]
		tag = ref[lastColon+1:]
		if repo == "" || tag == "" {
			return "", "", fmt.Errorf("invalid image ref %q", ref)
		}
		return repo, tag, nil
	}
	return ref, "latest", nil
}

func registryAuthHeader() string {
	return anonymousRegistryAuth
}
