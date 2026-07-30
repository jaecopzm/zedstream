package payments

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const moneyUnifyBaseURL = "https://api.moneyunify.one"

type Client struct {
	authID      string
	baseURL     string
	webhookHash string
	httpClient  *http.Client
}

func NewClient(authID, webhookHash string) *Client {
	return &Client{
		authID:      authID,
		baseURL:     moneyUnifyBaseURL,
		webhookHash: webhookHash,
		httpClient: &http.Client{
			Timeout: 15 * time.Second,
		},
	}
}

type PaymentRequest struct {
	Phone      string
	Amount     int
	TxRef      string
	WebhookURL string
}

type moneyUnifyRequestResponse struct {
	Message string `json:"message"`
	Data    *struct {
		Status         string  `json:"status"`
		Amount         int     `json:"amount"`
		TransactionID  string  `json:"transaction_id"`
		Charges        float64 `json:"charges"`
		FromPayer      string  `json:"from_payer"`
	} `json:"data"`
	IsError bool `json:"isError"`
}

func (c *Client) RequestPayment(ctx context.Context, req *PaymentRequest) (transactionID string, err error) {
	form := url.Values{
		"from_payer": {req.Phone},
		"amount":     {fmt.Sprintf("%d", req.Amount)},
		"auth_id":    {c.authID},
	}
	if req.WebhookURL != "" {
		form.Set("webhook_url", req.WebhookURL)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/payments/request", strings.NewReader(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	httpReq.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("moneyunify request payment: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read response: %w", err)
	}

	var result moneyUnifyRequestResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", fmt.Errorf("parse response: %w (body: %s)", err, string(respBody))
	}

	if result.IsError || result.Data == nil {
		return "", fmt.Errorf("moneyunify error: %s (body: %s)", result.Message, string(respBody))
	}

	return result.Data.TransactionID, nil
}

type moneyUnifyVerifyResponse struct {
	Message string `json:"message"`
	Data    *struct {
		Status        string `json:"status"`
		Amount        string `json:"amount"`
		TransactionID string `json:"transaction_id"`
		Charges       string `json:"charges"`
		FromPayer     string `json:"from_payer"`
	} `json:"data"`
	IsError bool `json:"isError"`
}

func (c *Client) VerifyTransaction(ctx context.Context, transactionID string) (status string, amount int, currency string, err error) {
	form := url.Values{
		"auth_id":        {c.authID},
		"transaction_id": {transactionID},
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/payments/verify", strings.NewReader(form.Encode()))
	if err != nil {
		return "", 0, "", fmt.Errorf("create verify request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	httpReq.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return "", 0, "", fmt.Errorf("moneyunify verify request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", 0, "", fmt.Errorf("read verify response: %w", err)
	}

	var result moneyUnifyVerifyResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", 0, "", fmt.Errorf("parse verify response: %w (body: %s)", err, string(respBody))
	}

	if result.IsError || result.Data == nil {
		return "", 0, "", fmt.Errorf("moneyunify verify error: %s (body: %s)", result.Message, string(respBody))
	}

	// Amount comes as string like "1.00", parse to int (ZMW subunits or whole)
	var amt int
	fmt.Sscanf(result.Data.Amount, "%d", &amt)

	return result.Data.Status, amt, "ZMW", nil
}

type WebhookPayload struct {
	Event string `json:"event"`
	Data  struct {
		ID        int    `json:"id"`
		TxRef     string `json:"tx_ref"`
		Amount    int    `json:"amount"`
		Currency  string `json:"currency"`
		Status    string `json:"status"`
	} `json:"data"`
}

func (c *Client) VerifyWebhookSignature(signature string) bool {
	return c.webhookHash == "" || signature == c.webhookHash
}
