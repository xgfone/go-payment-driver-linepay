// Copyright 2026 xgfone
// SPDX-License-Identifier: Apache-2.0

package linepay

import (
	"context"
	"net/http"

	"github.com/xgfone/go-payment-driver/driver"

	sdk "github.com/xgfone/go-payment-driver-linepay/linepay"
)

// ParseCallbackRequest checks a LINE Pay GET callback against the API. Once
// authentication is complete, it loads the trusted original order through
// LoadPayment and confirms the payment internally. LoadPayment is required
// for every callback. Browser-supplied amounts and statuses are never used
// to confirm a payment.
func (d *Driver) ParseCallbackRequest(ctx context.Context, req driver.CallbackRequest) (info driver.CallbackInfo, err error) {
	if req.LoadPayment == nil {
		return info, driver.ErrBadRequest.WithReason("LoadPayment is required to process LINE Pay callbacks")
	}

	if req.Type != "" && req.Type != driver.CallbackTypePayment {
		return info, driver.ErrUnsupported.WithReason("LINE Pay Online does not send refund callbacks")
	}

	if req.Request == nil || req.Request.URL == nil || req.Request.Method != http.MethodGet {
		return info, driver.ErrBadRequest.WithReason("LINE Pay redirect must be a GET request")
	}

	query, err := parseRedirectQuery(req.Request)
	if err != nil {
		return info, err
	}

	payment, ok, err := d.QueryPayment(ctx, query)
	if err != nil {
		return info, err
	}
	if !ok {
		return info, driver.ErrBadRequest.WithReason("LINE Pay payment not found")
	}

	if payment.TaskStatus == driver.TaskStatusProcessing &&
		payment.ChannelStatus == sdk.ReturnCodeAuthorized {
		order, found, err := req.LoadPayment(ctx, driver.PaymentReference{
			PaymentId:        query.PaymentId,
			ChannelPaymentId: query.ChannelPaymentId,
		})
		if err != nil {
			return info, err
		} else if !found {
			return info, driver.ErrBadRequest.WithReason("original payment order not found")
		}

		if order.PaymentId != query.PaymentId || order.ChannelPaymentId != query.ChannelPaymentId {
			return info, driver.ErrBadRequest.WithReason("callback does not match the original payment order")
		}

		payment, err = d.confirmPayment(ctx, order)
		if err != nil {
			return info, err
		}
	}

	return driver.CallbackInfo{
		Type:        driver.CallbackTypePayment,
		PaymentInfo: &payment,
	}, nil
}

func parseRedirectQuery(req *http.Request) (driver.QueryPaymentRequest, error) {
	query := req.URL.Query()
	if len(query["orderId"]) != 1 || len(query["transactionId"]) != 1 || query.Get("orderId") == "" {
		return driver.QueryPaymentRequest{}, driver.ErrBadRequest.WithReason("redirect requires one orderId and one transactionId")
	}
	if _, err := transactionId(query.Get("transactionId")); err != nil {
		return driver.QueryPaymentRequest{}, err
	}
	return driver.QueryPaymentRequest{
		PaymentId:        query.Get("orderId"),
		ChannelPaymentId: query.Get("transactionId"),
	}, nil
}

func (d *Driver) SendCallbackResponse(ctx context.Context, rw http.ResponseWriter, err error) {
	if err != nil {
		http.Error(rw, "LINE Pay payment verification failed", http.StatusInternalServerError)
		return
	}
	rw.WriteHeader(http.StatusOK)
}
