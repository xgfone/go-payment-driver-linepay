// Copyright 2026 xgfone
// SPDX-License-Identifier: Apache-2.0

package linepay

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
)

func transactionPath(id int64) (string, error) {
	if id <= 0 {
		return "", errors.New("linepay: transactionId must be positive")
	}
	return strconv.FormatInt(id, 10), nil
}

func (c *Client) RequestPayment(ctx context.Context, req RequestPaymentRequest) (*Response[RequestPaymentInfo], error) {
	return call[RequestPaymentInfo](ctx, c, http.MethodPost, "/v4/payments/request", nil, req, false)
}

func (c *Client) CheckPaymentRequestStatus(ctx context.Context, transactionId int64) (*Response[struct{}], error) {
	id, err := transactionPath(transactionId)
	if err != nil {
		return nil, err
	}
	return call[struct{}](ctx, c, http.MethodGet, "/v4/payments/requests/"+id+"/check", nil, nil, true)
}

func (c *Client) ConfirmPayment(ctx context.Context, transactionId int64, req ConfirmPaymentRequest) (*Response[ConfirmPaymentInfo], error) {
	id, err := transactionPath(transactionId)
	if err != nil {
		return nil, err
	}
	return call[ConfirmPaymentInfo](ctx, c, http.MethodPost, "/v4/payments/"+id+"/confirm", nil, req, false)
}

func (c *Client) Capture(ctx context.Context, transactionId int64, req CaptureRequest) (*Response[ConfirmPaymentInfo], error) {
	id, err := transactionPath(transactionId)
	if err != nil {
		return nil, err
	}
	return call[ConfirmPaymentInfo](ctx, c, http.MethodPost, "/v4/payments/authorizations/"+id+"/capture", nil, req, false)
}

func (c *Client) Void(ctx context.Context, transactionId int64) (*Response[struct{}], error) {
	id, err := transactionPath(transactionId)
	if err != nil {
		return nil, err
	}
	return call[struct{}](ctx, c, http.MethodPost, "/v4/payments/authorizations/"+id+"/void", nil, nil, false)
}

func (c *Client) RetrievePaymentDetails(ctx context.Context, req RetrievePaymentDetailsRequest) (*Response[[]PaymentDetails], error) {
	if len(req.OrderIds)+len(req.TransactionIds) == 0 {
		return nil, errors.New("linepay: orderId or transactionId is required")
	}

	query := make(url.Values)
	for _, id := range req.OrderIds {
		if id == "" {
			return nil, errors.New("linepay: orderId must not be empty")
		}
		query.Add("orderId", id)
	}

	for _, id := range req.TransactionIds {
		value, err := transactionPath(id)
		if err != nil {
			return nil, err
		}
		query.Add("transactionId", value)
	}

	return call[[]PaymentDetails](ctx, c, http.MethodGet, "/v4/payments", query, nil, false)
}

func (c *Client) Refund(ctx context.Context, transactionId int64, req RefundRequest) (*Response[RefundInfo], error) {
	id, err := transactionPath(transactionId)
	if err != nil {
		return nil, err
	}
	return call[RefundInfo](ctx, c, http.MethodPost, "/v4/payments/"+id+"/refund", nil, req, false)
}

func preapprovedPath(regKey string) (string, error) {
	if regKey == "" {
		return "", errors.New("linepay: regKey is required")
	}
	if regKey == "." || regKey == ".." {
		return "", errors.New("linepay: invalid regKey")
	}
	return "/v4/payments/preapprovedPay/" + url.PathEscape(regKey), nil
}

func (c *Client) CheckPreapprovedPaymentKeyStatus(ctx context.Context, regKey string, creditCardAuth *bool) (*Response[struct{}], error) {
	path, err := preapprovedPath(regKey)
	if err != nil {
		return nil, err
	}

	query := make(url.Values)
	if creditCardAuth != nil {
		query.Set("creditCardAuth", strconv.FormatBool(*creditCardAuth))
	}

	return call[struct{}](ctx, c, http.MethodGet, path+"/check", query, nil, false)
}

func (c *Client) RequestPreapprovedPayment(ctx context.Context, regKey string, req RequestPreapprovedPaymentRequest) (*Response[PreapprovedPaymentInfo], error) {
	path, err := preapprovedPath(regKey)
	if err != nil {
		return nil, err
	}
	return call[PreapprovedPaymentInfo](ctx, c, http.MethodPost, path+"/payment", nil, req, false)
}

func (c *Client) DiscardPreapprovedPaymentKey(ctx context.Context, regKey string) (*Response[struct{}], error) {
	path, err := preapprovedPath(regKey)
	if err != nil {
		return nil, err
	}
	return call[struct{}](ctx, c, http.MethodPost, path+"/expire", nil, nil, false)
}
