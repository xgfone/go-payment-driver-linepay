// Copyright 2026 xgfone
// SPDX-License-Identifier: Apache-2.0

package linepay

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func TestSign(t *testing.T) {
	got := Sign(
		"test-secret",
		"/v4/payments/request",
		`{"amount":100,"currency":"TWD","orderId":"訂單"}`,
		"01234567-89ab-4cde-8f01-23456789abcd",
	)
	if got != "hxeNhaY/Y8bOHyStmmvNw3fUu3XXNEqcnt8LX4004PI=" {
		t.Fatal(got)
	}
}

func TestOnlineEndpoints(t *testing.T) {
	const id int64 = 2023042201206549310
	var mu sync.Mutex
	seen := make(map[string]bool)
	nonces := make(map[string]bool)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		data := string(body)
		if r.Method == http.MethodGet {
			data = r.URL.RawQuery
			if len(body) != 0 {
				t.Error("GET body")
			}
		}

		nonce := r.Header.Get("X-LINE-Authorization-Nonce")
		if len(nonce) != 36 || nonce[14] != '4' {
			t.Errorf("invalid UUID v4: %q", nonce)
		}

		mac := hmac.New(sha256.New, []byte("secret"))
		_, _ = io.WriteString(mac, "secret"+r.URL.EscapedPath()+data+nonce)
		if r.Header.Get("X-LINE-Authorization") != base64.StdEncoding.EncodeToString(mac.Sum(nil)) {
			t.Error("signature does not match wire bytes")
		}
		if r.Header.Get("X-LINE-ChannelId") != "channel" ||
			r.Header.Get("Content-Type") != "application/json" ||
			r.Header.Get("X-LINE-MerchantDeviceType") != "web" ||
			r.Header.Get("X-LINE-MerchantDeviceProfileId") != "device" {
			t.Error("missing common headers")
		}

		mu.Lock()
		if nonces[nonce] {
			t.Error("reused nonce")
		}
		nonces[nonce] = true
		seen[r.Method+" "+r.URL.EscapedPath()] = true
		mu.Unlock()

		switch {
		case r.URL.Path == "/v4/payments/request":
			if !strings.Contains(string(body), `"amount":12.34`) {
				t.Error(string(body))
			}
			_, _ = io.WriteString(w, `{"returnCode":"0000","info":{"transactionId":2023042201206549310,"paymentUrl":{"web":"https://pay.example"}}}`)

		case r.URL.Path == "/v4/payments":
			if got := r.URL.Query()["orderId"]; len(got) != 2 || got[0] != "訂單&=?" || got[1] != "second" {
				t.Errorf("query %v", r.URL.Query())
			}
			if r.URL.Query().Get("transactionId") != "2023042201206549310" {
				t.Error("lost transaction precision")
			}
			_, _ = io.WriteString(w, `{"returnCode":"0000","info":[{"transactionId":2023042201206549310,"payInfo":[{"amount":12.34}]}]}`)

		case strings.HasSuffix(r.URL.Path, "/refund"):
			_, _ = io.WriteString(w, `{"returnCode":"0000","info":{"refundTransactionId":2023042201206549311,"refundTransactionDate":"2026-10-08T06:00:00Z"}}`)

		case strings.HasSuffix(r.URL.Path, "/void"), strings.HasSuffix(r.URL.Path, "/expire"):
			if len(body) != 0 {
				t.Error("bodyless endpoint sent JSON")
			}
			_, _ = io.WriteString(w, `{"returnCode":"0000"}`)

		case strings.HasSuffix(r.URL.Path, "/check"):
			if strings.Contains(r.URL.Path, "preapprovedPay") && r.URL.Query().Get("creditCardAuth") != "false" {
				t.Error("explicit false omitted")
			}
			_, _ = io.WriteString(w, `{"returnCode":"0000"}`)

		default:
			_, _ = io.WriteString(w, `{"returnCode":"0000","info":{"transactionId":2023042201206549310}}`)
		}
	}))
	defer server.Close()

	client, err := NewClient(Config{
		ChannelId:               "channel",
		ChannelSecret:           "secret",
		BaseUrl:                 server.URL,
		MerchantDeviceType:      "web",
		MerchantDeviceProfileId: "device",
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	created, err := client.RequestPayment(ctx, RequestPaymentRequest{
		Amount:   json.Number("12.34"),
		Currency: "USD",
		OrderId:  "訂單",
	})
	if err != nil || created.Info.TransactionId != id {
		t.Fatalf("create: %+v %v", created, err)
	}
	if _, err = client.CheckPaymentRequestStatus(ctx, id); err != nil {
		t.Fatal(err)
	}

	amount := ConfirmPaymentRequest{Amount: "12.34", Currency: "USD"}
	if _, err = client.ConfirmPayment(ctx, id, amount); err != nil {
		t.Fatal(err)
	}
	if _, err = client.Capture(ctx, id, amount); err != nil {
		t.Fatal(err)
	}
	if _, err = client.Void(ctx, id); err != nil {
		t.Fatal(err)
	}

	details, err := client.RetrievePaymentDetails(ctx, RetrievePaymentDetailsRequest{
		OrderIds:       []string{"訂單&=?", "second"},
		TransactionIds: []int64{id},
	})
	if err != nil || details.Info[0].PayInfo[0].Amount != "12.34" {
		t.Fatalf("details: %+v %v", details, err)
	}
	if _, err = client.Refund(ctx, id, RefundRequest{RefundAmount: "1.23"}); err != nil {
		t.Fatal(err)
	}

	auth := false
	if _, err = client.CheckPreapprovedPaymentKeyStatus(ctx, "key/?", &auth); err != nil {
		t.Fatal(err)
	}
	if _, err = client.RequestPreapprovedPayment(ctx, "key/?", RequestPreapprovedPaymentRequest{Amount: "12.34"}); err != nil {
		t.Fatal(err)
	}
	if _, err = client.DiscardPreapprovedPaymentKey(ctx, "key/?"); err != nil {
		t.Fatal(err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(seen) != 10 {
		t.Fatalf("expected ten endpoints, got %v", seen)
	}
}

func TestErrorsAndStatusCodes(t *testing.T) {
	for _, code := range []string{"0000", "0110", "0121", "0122", "0123", "1104"} {
		t.Run(code, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.WriteString(w, `{"returnCode":"`+code+`","returnMessage":"message"}`)
			}))
			defer server.Close()

			client, _ := NewClient(Config{
				ChannelId:     "channel",
				ChannelSecret: "secret",
				BaseUrl:       server.URL,
			})
			resp, err := client.CheckPaymentRequestStatus(context.Background(), 1)
			if code == "1104" {
				var apiErr *Error
				if !errors.As(err, &apiErr) || apiErr.ReturnCode != code {
					t.Fatalf("%v", err)
				}
			} else if err != nil || resp.ReturnCode != code {
				t.Fatalf("%+v %v", resp, err)
			}

			_, err = client.ConfirmPayment(context.Background(), 1, ConfirmPaymentRequest{
				Amount:   "1",
				Currency: "USD",
			})
			if (code == "0000") != (err == nil) {
				t.Fatalf("confirmation accepted status %s: %v", code, err)
			}
		})
	}

	for _, raw := range []string{`{}`, `null`, `{"returnCode":`, `{"returnCode":"0000"} trailing`} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.WriteString(w, raw)
		}))
		client, _ := NewClient(Config{
			ChannelId:     "channel",
			ChannelSecret: "secret",
			BaseUrl:       server.URL,
		})
		if _, err := client.CheckPaymentRequestStatus(context.Background(), 1); err == nil {
			t.Errorf("accepted malformed response %s", raw)
		}
		server.Close()
	}
}

func TestRedirectAndContext(t *testing.T) {
	var followed bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/leak" {
			followed = true
		}
		http.Redirect(w, r, "/leak", http.StatusFound)
	}))
	defer server.Close()

	client, _ := NewClient(Config{
		ChannelId:     "channel",
		ChannelSecret: "secret",
		BaseUrl:       server.URL,
	})
	_, err := client.CheckPaymentRequestStatus(context.Background(), 1)
	var httpErr *HttpError
	if !errors.As(err, &httpErr) || httpErr.StatusCode != 302 || followed {
		t.Fatalf("redirect: %v %v", err, followed)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := client.CheckPaymentRequestStatus(ctx, 1); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
