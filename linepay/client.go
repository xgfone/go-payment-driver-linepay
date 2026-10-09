// Copyright 2026 xgfone
// SPDX-License-Identifier: Apache-2.0

// Package linepay implements the LINE Pay Online API v4.
// Amounts are decimal major currency units, represented by json.Number to avoid
// floating-point rounding. Transaction identifiers are exact int64 values.
package linepay

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	SandboxBaseUrl    = "https://sandbox-api-pay.line.me"
	ProductionBaseUrl = "https://api-pay.line.me"

	ReturnCodeSuccess    = "0000"
	ReturnCodePending    = "0000"
	ReturnCodeAuthorized = "0110"
	ReturnCodeCanceled   = "0121"
	ReturnCodeFailed     = "0122"
	ReturnCodeCompleted  = "0123"
)

// Config configures a client. Set fields before constructing the client.
type Config struct {
	ChannelId     string
	ChannelSecret string

	MerchantDeviceType      string
	MerchantDeviceProfileId string

	// BaseUrl overrides the selected environment, for example for a test server.
	// HttpClient defaults to a client with a 70-second timeout. A shorter caller
	// deadline still takes precedence. Redirects are never followed.
	HttpClient *http.Client

	BaseUrl string
	Sandbox bool
}

// Client is safe for concurrent use. It does not retry requests automatically:
// a transport error on a mutating operation can have an unknown outcome.
type Client struct {
	config     Config
	httpClient *http.Client
}

func NewClient(config Config) (*Client, error) {
	if strings.TrimSpace(config.ChannelId) == "" ||
		strings.TrimSpace(config.ChannelSecret) == "" {
		return nil, errors.New("linepay: ChannelId and ChannelSecret are required")
	}

	if config.BaseUrl == "" {
		if config.Sandbox {
			config.BaseUrl = SandboxBaseUrl
		} else {
			config.BaseUrl = ProductionBaseUrl
		}
	}

	u, err := url.Parse(config.BaseUrl)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") ||
		u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" ||
		(u.Path != "" && u.Path != "/") {
		return nil, errors.New("linepay: BaseUrl must be an absolute HTTP(S) origin")
	}

	config.BaseUrl = strings.TrimSuffix(config.BaseUrl, "/")
	httpClient := http.Client{Timeout: 70 * time.Second}
	if config.HttpClient != nil {
		httpClient = *config.HttpClient
	}

	httpClient.CheckRedirect = checkRedirect
	return &Client{config: config, httpClient: &httpClient}, nil
}

func checkRedirect(*http.Request, []*http.Request) error {
	return http.ErrUseLastResponse
}

// Response retains the provider result code, including non-error status codes
// from CheckPaymentRequestStatus.
type Response[T any] struct {
	ReturnCode    string `json:"returnCode"`
	ReturnMessage string `json:"returnMessage"`
	Info          T      `json:"info"`
}

// Error is an unsuccessful API result, even when the HTTP status is 200.
type Error struct {
	ReturnCode    string
	ReturnMessage string
}

func (e *Error) Error() string {
	return fmt.Sprintf("linepay: %s: %s", e.ReturnCode, e.ReturnMessage)
}

// HttpError reports a non-200 response. Body is bounded by the response limit.
type HttpError struct {
	StatusCode int
	Body       string
}

func (e *HttpError) Error() string {
	return fmt.Sprintf("linepay: HTTP %d", e.StatusCode)
}

// Sign signs the exact query string (without '?') for GET, or exact JSON body
// for POST. The same bytes must be transmitted to the API.
func Sign(channelSecret, apiPath, data, nonce string) string {
	mac := hmac.New(sha256.New, []byte(channelSecret))
	_, _ = io.WriteString(mac, channelSecret+apiPath+data+nonce)
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

func newNonce() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}

	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:]), nil
}

func call[T any](
	ctx context.Context,
	c *Client,
	method,
	path string,
	query url.Values,
	body any,
	status bool,
) (*Response[T], error) {
	var err error
	var encoded []byte
	if body != nil {
		if encoded, err = json.Marshal(body); err != nil {
			return nil, fmt.Errorf("linepay: encode request: %w", err)
		}
	}

	data := string(encoded)
	requestUrl := c.config.BaseUrl + path
	if method == http.MethodGet {
		data = query.Encode()
		if data != "" {
			requestUrl += "?" + data
		}
	}

	nonce, err := newNonce()
	if err != nil {
		return nil, fmt.Errorf("linepay: nonce: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, method, requestUrl, bytes.NewReader(encoded))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-LINE-ChannelId", c.config.ChannelId)
	req.Header.Set("X-LINE-Authorization-Nonce", nonce)
	req.Header.Set("X-LINE-Authorization", Sign(c.config.ChannelSecret, path, data, nonce))
	if c.config.MerchantDeviceProfileId != "" {
		req.Header.Set("X-LINE-MerchantDeviceProfileId", c.config.MerchantDeviceProfileId)
	}
	if c.config.MerchantDeviceType != "" {
		req.Header.Set("X-LINE-MerchantDeviceType", c.config.MerchantDeviceType)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close() //nolint:errcheck

	const maxResponseSize = 4 << 20
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseSize+1))
	if err != nil {
		return nil, fmt.Errorf("linepay: read response: %w", err)
	}
	if len(raw) > maxResponseSize {
		return nil, errors.New("linepay: response exceeds 4 MiB")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, &HttpError{StatusCode: resp.StatusCode, Body: string(raw)}
	}

	var result Response[T]
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("linepay: decode response: %w", err)
	}
	if result.ReturnCode == "" {
		return nil, errors.New("linepay: response is missing returnCode")
	}
	if result.ReturnCode == ReturnCodeSuccess ||
		(status && (result.ReturnCode == ReturnCodeAuthorized ||
			result.ReturnCode == ReturnCodeCanceled ||
			result.ReturnCode == ReturnCodeFailed ||
			result.ReturnCode == ReturnCodeCompleted)) {
		return &result, nil
	}

	return &result, &Error{
		ReturnCode:    result.ReturnCode,
		ReturnMessage: result.ReturnMessage,
	}
}
