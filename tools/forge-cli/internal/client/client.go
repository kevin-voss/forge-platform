// Package client provides the shared Forge Control HTTP client.
package client

import (
	"fmt"
	"net/http"
	"net/url"
	"time"

	"forge.local/tools/forge-cli/internal/config"
)

// Client is the reusable HTTP client and Control base URL for future commands.
type Client struct {
	HTTP    *http.Client
	BaseURL *url.URL
}

// Response wraps an HTTP response with its correlation ID.
type Response struct {
	*http.Response
	RequestID string
}

// New creates a client with the configured base URL and request timeout.
func New(endpoint string, timeout time.Duration) (*Client, error) {
	if err := config.ValidateEndpoint(endpoint); err != nil {
		return nil, err
	}
	baseURL, err := url.Parse(endpoint)
	if err != nil {
		return nil, fmt.Errorf("parse endpoint: %w", err)
	}
	return &Client{
		HTTP:    &http.Client{Timeout: timeout},
		BaseURL: baseURL,
	}, nil
}

// Do sends a request and captures the Control response X-Request-Id header.
func (c *Client) Do(request *http.Request) (*Response, error) {
	response, err := c.HTTP.Do(request)
	if err != nil {
		return nil, err
	}
	return &Response{
		Response:  response,
		RequestID: response.Header.Get("X-Request-Id"),
	}, nil
}
