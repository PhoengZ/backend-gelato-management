package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
)

type Fulfillment interface {
	CheckSlotCapacity(ctx context.Context, slotStart time.Time) (int, error)
	// GetTicket reads the ticket for an order with the caller's own bearer
	// token, so Fulfillment applies its staff/manager authorization itself.
	GetTicket(ctx context.Context, orderID string) (*Ticket, error)
}

// ErrTicketNotFound means Fulfillment answered that no ticket exists (404).
var ErrTicketNotFound = errors.New("fulfillment ticket not found")

// ErrTicketForbidden means Fulfillment rejected the forwarded caller token (401/403).
var ErrTicketForbidden = errors.New("fulfillment rejected the caller credentials")

// Ticket holds the Fulfillment fields Order relies on.
type Ticket struct {
	OrderID string `json:"order_id"`
	Status  string `json:"status"`
	// QueueNumber is a zero-padded string such as "04" in the Fulfillment contract.
	QueueNumber string `json:"queue_number"`
}

func (c *fulfillmentClient) GetTicket(ctx context.Context, orderID string) (*Ticket, error) {
	id, err := uuid.Parse(orderID)
	if err != nil {
		return nil, fmt.Errorf("invalid order id: %w", err)
	}
	callCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(callCtx, http.MethodGet, c.baseURL+"/api/v1/fulfillments/"+id.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("create fulfillment request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	if bearer, ok := UserAuthorization(ctx); ok {
		req.Header.Set("Authorization", bearer)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fulfillment request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, ErrTicketNotFound
	}
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return nil, ErrTicketForbidden
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fulfillment service returned status %d", resp.StatusCode)
	}
	var ticket Ticket
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&ticket); err != nil {
		return nil, fmt.Errorf("decode fulfillment ticket: %w", err)
	}
	if ticket.Status == "" {
		return nil, errors.New("fulfillment ticket has no status")
	}
	return &ticket, nil
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
