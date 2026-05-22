package nomad

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

const defaultTimeout = 10 * time.Second

// Client is a minimal HTTP client for the Nomad API.
type Client struct {
	addr   string
	token  string
	http   *http.Client
}

// New returns a Nomad client targeting the given address.
// token may be empty for unauthenticated (dev) clusters.
func New(addr, token string) *Client {
	return &Client{
		addr:  addr,
		token: token,
		http:  &http.Client{Timeout: defaultTimeout},
	}
}

// Jobs fetches all jobs across all namespaces, returning summarised allocation counts.
func (c *Client) Jobs(ctx context.Context) ([]Job, error) {
	var stubs []jobListStub
	if err := c.get(ctx, "/v1/jobs?namespace=*", &stubs); err != nil {
		return nil, err
	}

	jobs := make([]Job, 0, len(stubs))
	for _, s := range stubs {
		j := Job{
			ID:        s.ID,
			Name:      s.Name,
			Namespace: s.Namespace,
			Type:      s.Type,
			Status:    s.Status,
		}
		if s.JobSummary != nil {
			for _, tg := range s.JobSummary.Summary {
				j.Allocs.Running += tg.Running
				j.Allocs.Queued += tg.Queued
				j.Allocs.Starting += tg.Starting
				j.Allocs.Failed += tg.Failed
				j.Allocs.Complete += tg.Complete
				j.Allocs.Lost += tg.Lost
				j.Allocs.Unknown += tg.Unknown
			}
		}
		jobs = append(jobs, j)
	}

	return jobs, nil
}

// Ping checks connectivity by fetching the agent info endpoint.
func (c *Client) Ping(ctx context.Context) error {
	var out interface{}
	return c.get(ctx, "/v1/agent/self", &out)
}

func (c *Client) get(ctx context.Context, path string, out interface{}) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.addr+path, nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}

	if c.token != "" {
		req.Header.Set("X-Nomad-Token", c.token)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("nomad request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("nomad API returned %s", resp.Status)
	}

	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}

	return nil
}
