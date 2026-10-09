// Copyright 2026 xgfone
// SPDX-License-Identifier: Apache-2.0

package linepay

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/xgfone/go-payment-driver/builder"
	"github.com/xgfone/go-payment-driver/driver"

	sdk "github.com/xgfone/go-payment-driver-linepay/linepay"
)

const testTransactionId = "2023042201206549310"
const testRefundId = "2023042201206549311"

func testOrder() driver.PaymentOrder {
	return driver.PaymentOrder{
		PaymentId:        "order",
		ChannelPaymentId: testTransactionId,
		PaymentAmount:    1234,
		PaymentCurrency:  "USD",
	}
}

func testCallbackRequest() driver.CallbackRequest {
	return driver.CallbackRequest{
		Request: httptest.NewRequest(http.MethodGet,
			"https://merchant.example/callback?orderId=order&transactionId="+testTransactionId+"&amount=999&currency=TWD&status=success", nil),
	}
}

func testDriver(t *testing.T, handler http.HandlerFunc) driver.Driver {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	d, err := NewDriver(Config{
		ChannelId:     "channel",
		ChannelSecret: "secret",

		Currencies: []string{"USD", "TWD", "THB"},

		ReturnUrl: "https://merchant.example/return?x=%2f#paid",
		CancelUrl: "https://merchant.example/cancel",
	})
	if err != nil {
		t.Fatal(err)
	}

	d.client, err = sdk.NewClient(sdk.Config{
		ChannelId:     "channel",
		ChannelSecret: "secret",
		BaseUrl:       server.URL,
	})
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func paymentJson(status, refunds string) string {
	return `{"returnCode":"0000","info":[{"transactionId":` + testTransactionId + `,"orderId":"order","transactionType":"PAYMENT","payStatus":"` + status + `","currency":"USD","transactionDate":"2026-10-08T06:00:00Z","payInfo":[{"method":"CREDIT_CARD","amount":10},{"method":"POINT","amount":2.34}],"refundList":` + refunds + `}]}`
}

func TestBuilderAndConfig(t *testing.T) {
	conf := `{"ChannelId":"channel","ChannelSecret":"secret","Sandbox":true,"Currencies":["usd","twd"]}`
	d, err := builder.BuildDriver("linepay_online", conf)
	if err != nil {
		t.Fatal(err)
	}

	metadata := d.Metadata()
	if metadata.Type != "linepay_online" ||
		metadata.LinkType != driver.LinkTypeCodeUrl ||
		strings.Join(metadata.Currencies, ",") != "USD,TWD" {
		t.Fatal(metadata)
	}

	metadata.Currencies[0] = "BROKEN"
	if d.Metadata().Currencies[0] != "USD" {
		t.Fatal("metadata is mutable")
	}

	parsed, err := builder.ParseConfig("linepay_online", conf)
	if err != nil || parsed.(Config).ChannelSecret != "***" {
		t.Fatalf("%+v %v", parsed, err)
	}

	for _, raw := range []string{
		`{}`,
		`{"ChannelId":"c","ChannelSecret":"s"}`,
		`{"ChannelId":"c","ChannelSecret":"s","Currencies":["JPY"]}`,
	} {
		if _, err := builder.BuildDriver("linepay_online", raw); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
}

func TestAmountConversion(t *testing.T) {
	for _, code := range []string{"USD", "TWD", "THB"} {
		t.Run(code, func(t *testing.T) {
			for _, amount := range []int64{1, 99, 100, 1234, 9223372036854775807} {
				major, err := majorAmount(amount, code)
				if err != nil {
					t.Fatal(err)
				}

				minor, err := minorAmount(major, code)
				if err != nil || minor != amount {
					t.Fatalf("%d -> %s -> %d: %v", amount, major, minor, err)
				}
			}
		})
	}

	for _, raw := range []json.Number{
		"",
		"1.001",
		"NaN",
		"92233720368547758.08",
		"1e9999999999",
	} {
		if _, err := minorAmount(raw, "USD"); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
	if amount, err := minorAmount("-1.23", "USD"); err != nil || amount != -123 {
		t.Fatalf("%d %v", amount, err)
	}
	if amount, err := minorAmount("1.234e1", "USD"); err != nil || amount != 1234 {
		t.Fatalf("scientific notation: %d %v", amount, err)
	}
	if amount, err := minorAmount("1.2300", "USD"); err != nil || amount != 123 {
		t.Fatalf("trailing zeroes: %d %v", amount, err)
	}
	// These helpers use the selected currency's precision, rather than a fixed
	// two-digit scale. The driver itself still only allows USD, TWD and THB.
	if amount, err := majorAmount(123, "JPY"); err != nil || amount != "123" {
		t.Fatalf("zero decimal currency: %s %v", amount, err)
	}
	if amount, err := minorAmount("1.234", "KWD"); err != nil || amount != 1234 {
		t.Fatalf("three decimal currency: %d %v", amount, err)
	}
	if _, err := minorAmount("1", "INVALID"); err == nil {
		t.Fatal("unsupported currency accepted")
	}
}

func TestCreatePayment(t *testing.T) {
	var calls int
	d := testDriver(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		var body sdk.RequestPaymentRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}

		if body.Amount != "12.34" ||
			body.Packages[0].Amount != body.Amount ||
			body.Packages[0].Products[0].Price != body.Amount ||
			body.Packages[0].Products[0].Quantity != 1 {
			t.Errorf("%+v", body)
		}
		if body.Currency != "USD" ||
			body.Options.Payment.Capture == nil ||
			!*body.Options.Payment.Capture {
			t.Errorf("%+v", body)
		}

		if body.RedirectUrls.ConfirmUrl != "https://merchant.example/return?x=%2f&PaymentId=order#paid" ||
			body.RedirectUrls.CancelUrl != "https://specific.example/cancel?keep=%2f" {
			t.Errorf("%+v", body.RedirectUrls)
		}
		_, _ = io.WriteString(w, `{"returnCode":"0000","info":{"transactionId":`+
			testTransactionId+
			`,"paymentUrl":{"web":"https://pay.example/web"}}}`)
	})

	info, err := d.CreatePayment(context.Background(), driver.CreatePaymentRequest{
		PaymentId:       "order",
		PaymentAmount:   1234,
		PaymentCurrency: "USD",
		CancelUrl:       "https://specific.example/cancel?keep=%2f",
		ExtInfo:         json.RawMessage(`{"Locale":"zh_TW"}`),
	})
	if err != nil ||
		info.ChannelPaymentId != testTransactionId ||
		info.PayLink != "https://pay.example/web" {
		t.Fatalf("%+v %v", info, err)
	}

	if info.ChannelData != "" {
		t.Fatalf("unexpected ChannelData: %s", info.ChannelData)
	}

	for _, req := range []driver.CreatePaymentRequest{
		{PaymentId: "order", PaymentCurrency: "USD", PaymentAmount: 0},
		{PaymentId: "order", PaymentCurrency: "JPY", PaymentAmount: 1234},
		{PaymentId: "order", PaymentCurrency: "USD", PaymentAmount: 1234, Share: true},
		{PaymentId: "order", PaymentCurrency: "USD", PaymentAmount: 1234, ExtInfo: json.RawMessage(`{"Locale":`)},
		{PaymentId: "order", PaymentCurrency: "USD", PaymentAmount: 1234, ReturnUrl: "/relative"},
		{PaymentId: "order", PaymentCurrency: "USD", PaymentAmount: 1234, CancelUrl: "/relative"},
	} {
		if _, err := d.CreatePayment(context.Background(), req); err == nil {
			t.Fatalf("accepted %+v", req)
		}
	}

	if calls != 1 {
		t.Fatalf("invalid requests reached API: %d", calls)
	}
}

func TestCreatePaymentIgnoresCallbackUrl(t *testing.T) {
	const callbackUrl = "https://merchant.example/payments/order/callback?keep=%2f"
	for _, tc := range []struct {
		name, callbackUrl, returnUrl, wantReturn string
	}{
		{"default return", callbackUrl, "", "https://merchant.example/return?x=%2f&PaymentId=order#paid"},
		{"request return", callbackUrl, "https://specific.example/return?keep=%2f#paid", "https://specific.example/return?keep=%2f#paid"},
		{"unused invalid callback", "/relative", "", "https://merchant.example/return?x=%2f&PaymentId=order#paid"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := testDriver(t, func(w http.ResponseWriter, r *http.Request) {
				var body sdk.RequestPaymentRequest
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				if body.RedirectUrls.ConfirmUrl != tc.wantReturn ||
					body.RedirectUrls.CancelUrl != "https://merchant.example/cancel?PaymentId=order" ||
					body.RedirectUrls.ConfirmUrlType != "CLIENT" {
					t.Error(body.RedirectUrls)
				}
				_, _ = io.WriteString(w, `{"returnCode":"0000","info":{"transactionId":`+testTransactionId+`,"paymentUrl":{"web":"https://pay.example/web"}}}`)
			})
			_, err := d.CreatePayment(context.Background(), driver.CreatePaymentRequest{
				PaymentId:       "order",
				PaymentAmount:   1234,
				PaymentCurrency: "USD",
				CallbackUrl:     tc.callbackUrl,
				ReturnUrl:       tc.returnUrl,
			})
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestCreatePaymentRequiresReturnUrl(t *testing.T) {
	var calls int
	d := testDriver(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusInternalServerError)
	})

	d.(*Driver).config.ReturnUrl = ""
	_, err := d.CreatePayment(context.Background(), driver.CreatePaymentRequest{
		PaymentId:       "order",
		PaymentAmount:   1234,
		PaymentCurrency: "USD",
		CallbackUrl:     "https://merchant.example/payments/order/callback",
	})
	if !errors.Is(err, driver.ErrBadRequest) ||
		!strings.Contains(err.Error(), "missing ReturnUrl") {
		t.Fatalf("CallbackUrl must not substitute for ReturnUrl: %v", err)
	}
	if calls != 0 {
		t.Fatalf("missing ReturnUrl reached LINE Pay API: %d", calls)
	}
}

func TestCreatePaymentRedirectOverrides(t *testing.T) {
	const requestReturn = "https://specific.example/return?x=%2f&x=2#paid"
	const requestCancel = "https://specific.example/cancel?keep=%2f#canceled"
	for _, tc := range []struct {
		name, returnUrl, cancelUrl, wantReturn, wantCancel string
	}{
		{"defaults", "", "", "https://merchant.example/return?x=%2f&PaymentId=order+%2B%2F%3F%26%23#paid", "https://merchant.example/cancel?PaymentId=order+%2B%2F%3F%26%23"},
		{"return override", requestReturn, "", requestReturn, "https://merchant.example/cancel?PaymentId=order+%2B%2F%3F%26%23"},
		{"cancel override", "", requestCancel, "https://merchant.example/return?x=%2f&PaymentId=order+%2B%2F%3F%26%23#paid", requestCancel},
		{"both overrides", requestReturn, requestCancel, requestReturn, requestCancel},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls int
			d := testDriver(t, func(w http.ResponseWriter, r *http.Request) {
				calls++
				var body sdk.RequestPaymentRequest
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				if body.RedirectUrls.ConfirmUrl != tc.wantReturn ||
					body.RedirectUrls.CancelUrl != tc.wantCancel {
					t.Error(body.RedirectUrls)
				}
				_, _ = io.WriteString(w, `{"returnCode":"0000","info":{"transactionId":`+testTransactionId+`,"paymentUrl":{"web":"https://pay.example/web"}}}`)
			})
			// Repeated creation must not append parameters to the saved defaults.
			for range 2 {
				_, err := d.CreatePayment(context.Background(), driver.CreatePaymentRequest{
					PaymentId:       "order +/?&#",
					PaymentAmount:   1234,
					PaymentCurrency: "USD",
					ReturnUrl:       tc.returnUrl,
					CancelUrl:       tc.cancelUrl,
				})
				if err != nil {
					t.Fatal(err)
				}
			}
			if calls != 2 {
				t.Fatal(calls)
			}
		})
	}
}

func TestCallbackRequiresLoaderAtEntry(t *testing.T) {
	var calls int
	d := testDriver(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusInternalServerError)
	})
	for _, req := range []driver.CallbackRequest{
		testCallbackRequest(),
		{},
		{Type: driver.CallbackTypeRefund},
		{Request: httptest.NewRequest(http.MethodPost, "https://merchant.example/callback", nil)},
	} {
		_, err := d.ParseCallbackRequest(context.Background(), req)
		if !errors.Is(err, driver.ErrBadRequest) || !strings.Contains(err.Error(), "LoadPayment") {
			t.Fatalf("missing loader: %v", err)
		}
	}
	if calls != 0 {
		t.Fatalf("missing loader reached LINE Pay API: %d", calls)
	}
}

func TestCallbackConfirmsAuthenticatedPayment(t *testing.T) {
	var captured bool
	var confirmed, loaded int
	d := testDriver(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v4/payments":
			if captured {
				_, _ = io.WriteString(w, paymentJson("CAPTURE", "[]"))
			} else {
				_, _ = io.WriteString(w, `{"returnCode":"1150"}`)
			}
		case "/v4/payments/requests/" + testTransactionId + "/check":
			_, _ = io.WriteString(w, `{"returnCode":"0110"}`)
		case "/v4/payments/" + testTransactionId + "/confirm":
			confirmed++
			var body sdk.ConfirmPaymentRequest
			_ = json.NewDecoder(r.Body).Decode(&body)
			if r.Method != http.MethodPost || body.Amount != "12.34" || body.Currency != "USD" {
				t.Error(r.Method, body)
			}
			captured = true
			_, _ = io.WriteString(w, `{"returnCode":"0000","info":{"transactionId":`+
				testTransactionId+`,"orderId":"order","payInfo":[{"amount":12.34}]}}`)
		default:
			t.Errorf("unexpected request %s", r.URL.Path)
		}
	})
	query := driver.QueryPaymentRequest{PaymentId: "order", ChannelPaymentId: testTransactionId}
	info, ok, err := d.QueryPayment(context.Background(), query)
	if err != nil || !ok || info.TaskStatus != driver.TaskStatusProcessing || confirmed != 0 {
		t.Fatalf("%+v %v %v confirms=%d", info, ok, err, confirmed)
	}

	request := testCallbackRequest()
	request.LoadPayment = func(ctx context.Context, ref driver.PaymentReference) (driver.PaymentOrder, bool, error) {
		loaded++
		if ref.PaymentId != "order" || ref.ChannelPaymentId != testTransactionId {
			t.Error(ref)
		}
		order := testOrder()
		order.PaymentCurrency = "usd"
		return order, true, nil
	}
	callback, err := d.ParseCallbackRequest(context.Background(), request)
	if err != nil || callback.PaymentInfo == nil {
		t.Fatalf("%+v %v", callback, err)
	}
	info = *callback.PaymentInfo
	if info.TaskStatus != driver.TaskStatusSuccess || info.PayerPaidAmount != 1234 ||
		info.PayerPaidCurrency != "USD" || !info.PayerPaidAt.IsZero() || info.ChannelData != "" {
		t.Fatal(info)
	}
	info, ok, err = d.QueryPayment(context.Background(), query)
	if err != nil || !ok || info.PayerPaidAt.IsZero() {
		t.Fatalf("%+v %v %v", info, ok, err)
	}
	callback, err = d.ParseCallbackRequest(context.Background(), request)
	if err != nil || callback.PaymentInfo.TaskStatus != driver.TaskStatusSuccess || confirmed != 1 || loaded != 1 {
		t.Fatalf("%+v %v confirms=%d loads=%d", callback, err, confirmed, loaded)
	}
}

func TestQueryStatusesAndCancellation(t *testing.T) {
	for _, tc := range []struct {
		code   string
		status driver.TaskStatus
	}{
		{"0000", driver.TaskStatusProcessing},
		{"0110", driver.TaskStatusProcessing},
		{"0121", driver.TaskStatusClosed},
		{"0122", driver.TaskStatusFailure},
	} {
		t.Run(tc.code, func(t *testing.T) {
			d := testDriver(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet {
					t.Error("query/cancel confirmed payment")
				}
				if r.URL.Path == "/v4/payments" {
					_, _ = io.WriteString(w, `{"returnCode":"1150"}`)
				} else {
					_, _ = io.WriteString(w, `{"returnCode":"`+tc.code+`"}`)
				}
			})

			info, ok, err := d.QueryPayment(context.Background(), driver.QueryPaymentRequest{
				PaymentId:        "order",
				ChannelPaymentId: testTransactionId,
			})
			if err != nil || !ok || info.TaskStatus != tc.status {
				t.Fatalf("%+v %v %v", info, ok, err)
			}

			err = d.CancelPayment(context.Background(), driver.CancelPaymentRequest{
				PaymentId:        "order",
				ChannelPaymentId: testTransactionId,
			})
			if tc.status == driver.TaskStatusProcessing {
				if !errors.Is(err, driver.ErrUnsupported) {
					t.Fatal(err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestPartialAndFullRefunds(t *testing.T) {
	for _, tc := range []struct {
		refunds string
		full    bool
	}{{`[{"refundAmount":-1.23}]`, false}, {`[{"refundAmount":-1.23},{"refundAmount":-11.11}]`, true}} {
		d := testDriver(t, func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.WriteString(w, paymentJson("CAPTURE", tc.refunds))
		})

		info, ok, err := d.QueryPayment(context.Background(), driver.QueryPaymentRequest{
			PaymentId:        "order",
			ChannelPaymentId: testTransactionId,
		})
		if err != nil || !ok || info.IsRefunded != tc.full || info.PayerPaidAmount != 1234 {
			t.Fatalf("%+v %v %v", info, ok, err)
		}

		err = d.CancelPayment(context.Background(), driver.CancelPaymentRequest{
			PaymentId:        "order",
			ChannelPaymentId: testTransactionId,
		})
		if !errors.Is(err, driver.ErrPaid) {
			t.Fatal(err)
		}
		if tc.full {
			_, err := d.RefundPayment(context.Background(), driver.CreateRefundRequest{
				PaymentId:        "order",
				RefundId:         "refund",
				PaymentAmount:    1234,
				RefundAmount:     123,
				PaymentCurrency:  "USD",
				ChannelPaymentId: testTransactionId,
			})
			if !errors.Is(err, driver.ErrPaymentRefundedFully) {
				t.Fatal(err)
			}
		}
	}
}

func TestRefundLifecycle(t *testing.T) {
	var refundCalls int
	d := testDriver(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/refund") {
			refundCalls++
			var body sdk.RefundRequest
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body.RefundAmount != "1.23" {
				t.Error(body)
			}
			_, _ = io.WriteString(w, `{"returnCode":"0000","info":{"refundTransactionId":`+testRefundId+`,"refundTransactionDate":"2026-10-08T06:01:00Z"}}`)
		} else if r.URL.Query().Get("transactionId") == testRefundId {
			_, _ = io.WriteString(w, `{"returnCode":"0000","info":[{"transactionId":`+testRefundId+`,"originalTransactionId":`+testTransactionId+`,"orderId":"order","transactionType":"PARTIAL_REFUND","transactionDate":"2026-10-08T06:01:00Z"}]}`)
		} else {
			_, _ = io.WriteString(w, paymentJson("CAPTURE", "[]"))
		}
	})

	created, err := d.RefundPayment(context.Background(), driver.CreateRefundRequest{
		PaymentId:        "order",
		RefundId:         "refund",
		RefundReason:     "reason",
		PaymentAmount:    1234,
		PaymentCurrency:  "USD",
		RefundAmount:     123,
		ChannelPaymentId: testTransactionId,
	})
	if err != nil || created.TaskStatus != driver.TaskStatusSuccess ||
		created.ChannelRefundId != testRefundId || created.RefundedAt.IsZero() ||
		created.ChannelData != "" || created.RefundReason != "reason" {
		t.Fatalf("%+v %v", created, err)
	}

	queried, ok, err := d.QueryRefund(context.Background(), driver.QueryRefundRequest{
		PaymentId:        "order",
		RefundId:         "refund",
		ChannelPaymentId: testTransactionId,
		ChannelRefundId:  created.ChannelRefundId,
	})
	if err != nil || !ok ||
		queried.ChannelRefundId != testRefundId ||
		queried.RefundId != "refund" || queried.RefundReason != "" || queried.ChannelData != "" ||
		!queried.RefundedAt.Equal(created.RefundedAt) {
		t.Fatalf("%+v %v %v", queried, ok, err)
	}

	_, _, err = d.QueryRefund(context.Background(), driver.QueryRefundRequest{
		PaymentId:        "order",
		RefundId:         "refund",
		ChannelPaymentId: testTransactionId,
	})
	if !errors.Is(err, driver.ErrUnsupported) {
		t.Fatal(err)
	}

	_, err = d.RefundPayment(context.Background(), driver.CreateRefundRequest{
		PaymentId:        "order",
		RefundId:         "refund",
		PaymentAmount:    1234,
		PaymentCurrency:  "USD",
		RefundAmount:     1235,
		ChannelPaymentId: testTransactionId,
	})
	if err == nil {
		t.Fatal("invalid amount")
	}
	if refundCalls != 1 {
		t.Fatal(refundCalls)
	}
}

func TestCallbackChecksProviderState(t *testing.T) {
	d := testDriver(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatal("callback tried to confirm")
		}
		_, _ = io.WriteString(w, paymentJson("CAPTURE", "[]"))
	})
	loader := func(context.Context, driver.PaymentReference) (driver.PaymentOrder, bool, error) {
		t.Error("captured payment should not load an order")
		return driver.PaymentOrder{}, false, io.ErrUnexpectedEOF
	}

	req := httptest.NewRequest(http.MethodGet, "https://merchant.example/return?orderId=order&transactionId="+testTransactionId+"&amount=999&status=success", nil)
	info, err := d.ParseCallbackRequest(context.Background(), driver.CallbackRequest{Request: req, LoadPayment: loader})
	if err != nil || info.PaymentInfo.PayerPaidAmount != 1234 ||
		info.PaymentInfo.TaskStatus != driver.TaskStatusSuccess {
		t.Fatalf("%+v %v", info, err)
	}

	req = httptest.NewRequest(http.MethodGet, "https://merchant.example/return?orderId=forged&transactionId="+testTransactionId, nil)
	if _, err := d.ParseCallbackRequest(context.Background(), driver.CallbackRequest{Request: req, LoadPayment: loader}); err == nil {
		t.Fatal("accepted forged orderId")
	}

	for _, path := range []string{"?orderId=order", "?orderId=order&transactionId=abc", "?orderId=order&transactionId=1&transactionId=2"} {
		_, err := d.ParseCallbackRequest(context.Background(), driver.CallbackRequest{
			Request:     httptest.NewRequest("GET", "https://merchant.example/return"+path, nil),
			LoadPayment: loader,
		})
		if err == nil {
			t.Fatal(path)
		}
	}

	response := httptest.NewRecorder()
	d.SendCallbackResponse(context.Background(), response, nil)
	if response.Code != 200 {
		t.Fatal(response.Code)
	}

	response = httptest.NewRecorder()
	d.SendCallbackResponse(context.Background(), response, errors.New("failed"))
	if response.Code != 500 {
		t.Fatal(response.Code)
	}
}

func TestConcurrentConfirmationRecovery(t *testing.T) {
	var mu sync.Mutex
	var captured bool
	var confirms int
	barrier := make(chan struct{})
	d := testDriver(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v4/payments" {
			mu.Lock()
			done := captured
			mu.Unlock()
			if done {
				_, _ = io.WriteString(w, paymentJson("CAPTURE", "[]"))
			} else {
				_, _ = io.WriteString(w, `{"returnCode":"1150"}`)
			}
		} else if strings.HasSuffix(r.URL.Path, "/check") {
			_, _ = io.WriteString(w, `{"returnCode":"0110"}`)
		} else {
			mu.Lock()
			confirms++
			n := confirms
			if n == 2 {
				close(barrier)
			}
			mu.Unlock()

			select {
			case <-barrier:
			case <-time.After(5 * time.Second):
				t.Error("confirmation concurrency timed out")
			}

			mu.Lock()
			already := captured
			captured = true
			mu.Unlock()

			if already {
				_, _ = io.WriteString(w, `{"returnCode":"1172"}`)
			} else {
				_, _ = io.WriteString(w, `{"returnCode":"0000","info":{"transactionId":`+
					testTransactionId+
					`,"orderId":"order","payInfo":[{"amount":12.34}]}}`)
			}
		}
	})

	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			request := testCallbackRequest()
			request.LoadPayment = func(context.Context, driver.PaymentReference) (driver.PaymentOrder, bool, error) {
				return testOrder(), true, nil
			}
			info, err := d.ParseCallbackRequest(context.Background(), request)
			if err != nil || info.PaymentInfo == nil || info.PaymentInfo.TaskStatus != driver.TaskStatusSuccess {
				t.Errorf("%+v %v", info, err)
			}
		}()
	}
	wg.Wait()
}

func TestQueriesIgnoreChannelData(t *testing.T) {
	d := testDriver(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Error("unexpected mutation")
		}
		if r.URL.Path == "/v4/payments" {
			_, _ = io.WriteString(w, `{"returnCode":"1150"}`)
		} else {
			_, _ = io.WriteString(w, `{"returnCode":"0110"}`)
		}
	})

	for _, data := range []string{
		"",
		"broken",
		`{"PaymentId":"different","PaymentAmount":999,"PaymentCurrency":"TWD"}`,
	} {
		info, ok, err := d.QueryPayment(context.Background(), driver.QueryPaymentRequest{
			PaymentId:        "order",
			ChannelPaymentId: testTransactionId,
			ChannelData:      data,
		})
		if err != nil || !ok || info.TaskStatus != driver.TaskStatusProcessing ||
			info.PaymentId != "order" || info.ChannelData != "" {
			t.Fatalf("%+v %v %v", info, ok, err)
		}
	}

	for _, req := range []driver.QueryPaymentRequest{
		{},
		{ChannelData: `{"PaymentId":"order","TransactionId":"` + testTransactionId + `"}`},
		{PaymentId: "order", ChannelPaymentId: "-1"},
		{PaymentId: "order", ChannelPaymentId: "01"},
	} {
		if _, _, err := d.QueryPayment(context.Background(), req); err == nil {
			t.Fatalf("accepted %+v", req)
		}
	}
}

func TestCallbackRequiresTrustedOriginalOrder(t *testing.T) {
	d := testDriver(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Error("invalid order reached confirmation API")
		}
		if r.URL.Path == "/v4/payments" {
			_, _ = io.WriteString(w, `{"returnCode":"1150"}`)
		} else {
			_, _ = io.WriteString(w, `{"returnCode":"0110"}`)
		}
	})
	for _, order := range []driver.PaymentOrder{
		{},
		{ChannelPaymentId: testTransactionId, PaymentAmount: 1234, PaymentCurrency: "USD"},
		{PaymentId: "order", PaymentAmount: 1234, PaymentCurrency: "USD"},
		{PaymentId: "different", ChannelPaymentId: testTransactionId, PaymentAmount: 1234, PaymentCurrency: "USD"},
		{PaymentId: "order", ChannelPaymentId: "1", PaymentAmount: 1234, PaymentCurrency: "USD"},
		{PaymentId: "order", ChannelPaymentId: testTransactionId, PaymentCurrency: "USD"},
		{PaymentId: "order", ChannelPaymentId: testTransactionId, PaymentAmount: -1, PaymentCurrency: "USD"},
		{PaymentId: "order", ChannelPaymentId: testTransactionId, PaymentAmount: 1234},
		{PaymentId: "order", ChannelPaymentId: testTransactionId, PaymentAmount: 1234, PaymentCurrency: "JPY"},
	} {
		request := testCallbackRequest()
		request.LoadPayment = func(context.Context, driver.PaymentReference) (driver.PaymentOrder, bool, error) {
			return order, true, nil
		}
		if _, err := d.ParseCallbackRequest(context.Background(), request); err == nil {
			t.Fatalf("accepted %+v", order)
		}
	}

	for _, loader := range []driver.PaymentLoader{
		nil,
		func(context.Context, driver.PaymentReference) (driver.PaymentOrder, bool, error) {
			return driver.PaymentOrder{}, false, nil
		},
		func(context.Context, driver.PaymentReference) (driver.PaymentOrder, bool, error) {
			return driver.PaymentOrder{}, false, io.ErrUnexpectedEOF
		},
	} {
		request := testCallbackRequest()
		request.LoadPayment = loader
		if _, err := d.ParseCallbackRequest(context.Background(), request); err == nil {
			t.Fatal("confirmed without trusted order")
		}
	}
}

func TestCallbackValidatesConfirmationResponse(t *testing.T) {
	for _, response := range []string{
		`{"transactionId":` + testTransactionId + `,"orderId":"different","payInfo":[{"amount":12.34}]}`,
		`{"transactionId":1,"orderId":"order","payInfo":[{"amount":12.34}]}`,
		`{"transactionId":` + testTransactionId + `,"orderId":"order","payInfo":[{"amount":12.35}]}`,
		`{"transactionId":` + testTransactionId + `,"orderId":"order","authorizationExpireDate":"2026-11-08T06:00:00Z","payInfo":[{"amount":12.34}]}`,
	} {
		d := testDriver(t, func(w http.ResponseWriter, r *http.Request) {
			switch {
			case r.URL.Path == "/v4/payments":
				_, _ = io.WriteString(w, `{"returnCode":"1150"}`)
			case strings.HasSuffix(r.URL.Path, "/check"):
				_, _ = io.WriteString(w, `{"returnCode":"0110"}`)
			default:
				_, _ = io.WriteString(w, `{"returnCode":"0000","info":`+response+`}`)
			}
		})
		request := testCallbackRequest()
		request.LoadPayment = func(context.Context, driver.PaymentReference) (driver.PaymentOrder, bool, error) {
			return testOrder(), true, nil
		}
		if _, err := d.ParseCallbackRequest(context.Background(), request); err == nil {
			t.Fatalf("accepted confirmation: %s", response)
		}
	}
}

func TestCallbackConfirmsOnlyAfterAuthentication(t *testing.T) {
	for _, tc := range []struct {
		code   string
		status driver.TaskStatus
	}{
		{"0000", driver.TaskStatusProcessing},
		{"0121", driver.TaskStatusClosed},
		{"0122", driver.TaskStatusFailure},
		{"0110", driver.TaskStatusProcessing},
	} {
		t.Run(tc.code, func(t *testing.T) {
			var confirms, loads int
			d := testDriver(t, func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.URL.Path == "/v4/payments":
					_, _ = io.WriteString(w, `{"returnCode":"1150"}`)
				case strings.HasSuffix(r.URL.Path, "/check"):
					_, _ = io.WriteString(w, `{"returnCode":"`+tc.code+`"}`)
				default:
					confirms++
					_, _ = io.WriteString(w, `{"returnCode":"1172"}`)
				}
			})
			request := testCallbackRequest()
			request.LoadPayment = func(context.Context, driver.PaymentReference) (driver.PaymentOrder, bool, error) {
				loads++
				return testOrder(), true, nil
			}
			info, err := d.ParseCallbackRequest(context.Background(), request)
			if tc.code == "0110" {
				if err == nil || confirms != 1 || loads != 1 {
					t.Fatalf("%+v %v confirms=%d loads=%d", info, err, confirms, loads)
				}
			} else if err != nil || info.PaymentInfo == nil || info.PaymentInfo.TaskStatus != tc.status || confirms != 0 || loads != 0 {
				t.Fatalf("%+v %v confirms=%d loads=%d", info, err, confirms, loads)
			}
		})
	}
}

func TestRefundQueryUsesExplicitReferences(t *testing.T) {
	for _, fallback := range []bool{false, true} {
		d := testDriver(t, func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet {
				t.Error("refund query modified payment")
			}
			if r.URL.Query().Get("transactionId") == testRefundId {
				if fallback {
					_, _ = io.WriteString(w, `{"returnCode":"1150"}`)
				} else {
					_, _ = io.WriteString(w, `{"returnCode":"0000","info":[{"transactionId":`+testRefundId+`,"originalTransactionId":`+testTransactionId+`,"orderId":"order","transactionType":"PARTIAL_REFUND","transactionDate":"2026-10-08T06:01:00Z"}]}`)
				}
			} else {
				_, _ = io.WriteString(w, paymentJson("CAPTURE", `[{"refundTransactionId":`+testRefundId+`,"transactionType":"PARTIAL_REFUND","refundAmount":-1.23,"refundTransactionDate":"2026-10-08T06:01:00Z"}]`))
			}
		})
		req := driver.QueryRefundRequest{
			PaymentId:        "order",
			RefundId:         "refund",
			ChannelPaymentId: testTransactionId,
			ChannelRefundId:  testRefundId,
			ChannelData:      "unused",
		}
		info, ok, err := d.QueryRefund(context.Background(), req)
		if err != nil || !ok || info.ChannelRefundId != testRefundId ||
			info.RefundId != "refund" || info.TaskStatus != driver.TaskStatusSuccess ||
			info.ChannelData != "" || info.RefundedAt.IsZero() {
			t.Fatalf("fallback=%v %+v %v %v", fallback, info, ok, err)
		}

		req.PaymentId = "different"
		if _, _, err := d.QueryRefund(context.Background(), req); err == nil {
			t.Fatal("accepted mismatched payment Id")
		}

		if !fallback {
			req.PaymentId = "order"
			req.ChannelPaymentId = "1"
			if _, _, err := d.QueryRefund(context.Background(), req); err == nil {
				t.Fatal("accepted mismatched original transaction Id")
			}
		}
	}
}
