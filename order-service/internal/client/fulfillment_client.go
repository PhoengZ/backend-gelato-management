package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type Fulfillment interface {
	CheckSlotCapacity(ctx context.Context, slotStart time.Time) (int, error)
}

type fulfillmentClient struct {
	baseURL    string
	httpClient *http.Client
	timeout    time.Duration
}

func NewFulfillmentClient(baseURL string, timeout time.Duration) (Fulfillment, error) {
	if baseURL == "" {
		return nil, errors.New("fulfillment base URL is required")
	}
	baseURL = strings.TrimRight(baseURL, "/")
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	return &fulfillmentClient{
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout: timeout,
		},
		timeout: timeout,
	}, nil
}

func (c *fulfillmentClient) CheckSlotCapacity(ctx context.Context, slotStart time.Time) (int, error) {
	callCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	reqURL, err := url.Parse(c.baseURL + "/api/v1/fulfillments/slots/capacity")
	if err != nil {
		return 0, fmt.Errorf("invalid fulfillment URL: %w", err)
	}
	q := reqURL.Query()
	formattedTime := slotStart.UTC().Format(time.RFC3339)
	q.Set("slot_start", formattedTime)
	q.Set("slot_time", formattedTime)
	reqURL.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(callCtx, http.MethodGet, reqURL.String(), nil)
	if err != nil {
		return 0, fmt.Errorf("create fulfillment request: %w", err)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("fulfillment request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return 0, nil
	}

	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("fulfillment service returned status %d", resp.StatusCode)
	}

	var data struct {
		ActiveTicketCount int `json:"active_ticket_count"`
		ActiveCount       int `json:"active_count"`
		Capacity          int `json:"capacity"`
		Count             int `json:"count"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return 0, fmt.Errorf("decode fulfillment response: %w", err)
	}

	count := data.ActiveTicketCount
	if count == 0 && data.ActiveCount > 0 {
		count = data.ActiveCount
	}
	if count == 0 && data.Capacity > 0 {
		count = data.Capacity
	}
	if count == 0 && data.Count > 0 {
		count = data.Count
	}

	return count, nil
}
