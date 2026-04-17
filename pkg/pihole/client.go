package pihole

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

func (c *AuthClient) GetJSONMap(ctx context.Context, path string) (map[string]any, error) {
	path = "/" + strings.TrimLeft(path, "/")

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.resolve("/api"+path), nil)
	if err != nil {
		return nil, fmt.Errorf("create pihole request: %w", err)
	}
	req.Header.Set("Accept", "application/json")

	if err := c.AddAuthHeaders(ctx, req); err != nil {
		return nil, err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("query pihole %s: %w", path, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		var errResp apiErrorResponse
		if decodeErr := json.NewDecoder(resp.Body).Decode(&errResp); decodeErr == nil && errResp.Error.Message != "" {
			return nil, fmt.Errorf("query pihole %s: %s: %s", path, resp.Status, errResp.Error.Message)
		}
		return nil, fmt.Errorf("query pihole %s: %s", path, resp.Status)
	}

	decoder := json.NewDecoder(resp.Body)
	decoder.UseNumber()

	var out map[string]any
	if err := decoder.Decode(&out); err != nil {
		return nil, fmt.Errorf("decode pihole %s response: %w", path, err)
	}

	return out, nil
}
