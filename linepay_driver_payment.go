// Copyright 2026 xgfone
// SPDX-License-Identifier: Apache-2.0

package linepay

import (
	"cmp"
	"context"
	"errors"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/xgfone/go-payment-driver/driver"

	sdk "github.com/xgfone/go-payment-driver-linepay/linepay"
)

func (d *Driver) CreatePayment(ctx context.Context, req driver.CreatePaymentRequest) (info driver.PayLinkInfo, err error) {
	if req.PaymentId == "" || utf8.RuneCountInString(req.PaymentId) > 100 {
		return info, driver.ErrBadRequest.WithReason("PaymentId must contain 1 to 100 characters")
	}
	if req.Share {
		return info, driver.ErrUnsupported.WithReason("LINE Pay Online does not support profit sharing")
	}

	currency := strings.ToUpper(req.PaymentCurrency)
	if !d.metadata.CurrencyIsSupported(currency) {
		return info, driver.ErrUnsupported.WithReasonf("unsupported currency %q", currency)
	}

	amount, err := majorAmount(req.PaymentAmount, currency)
	if err != nil {
		return info, err
	}

	confirmUrl := returnUrl(req.ReturnUrl, d.config.ReturnUrl, req.PaymentId)
	cancelUrl := returnUrl(req.CancelUrl, d.config.CancelUrl, req.PaymentId)

	for _, field := range []struct{ name, value string }{
		{"ReturnUrl", confirmUrl},
		{"CancelUrl", cancelUrl},
	} {
		if field.value == "" {
			return info, driver.ErrBadRequest.WithReason("missing " + field.name)
		}
		if err := validateUrl(field.name, field.value); err != nil {
			return info, driver.ErrBadRequest.WithError(err)
		}
	}

	options, err := checkoutOptions(req.ExtInfo)
	if err != nil {
		return info, driver.ErrBadRequest.WithError(err)
	}

	name := cmp.Or(req.PaymentDesc, req.PaymentId)
	if utf8.RuneCountInString(name) > 4000 {
		return info, driver.ErrBadRequest.WithReason("PaymentDesc exceeds 4000 characters")
	}

	capture := true
	resp, err := d.client.RequestPayment(ctx, sdk.RequestPaymentRequest{
		Amount:   amount,
		Currency: currency,
		OrderId:  req.PaymentId,
		Packages: []sdk.Package{
			{
				Id:     "1",
				Amount: amount,
				Products: []sdk.Product{
					{
						Name:     name,
						Price:    amount,
						Quantity: 1,
					},
				},
			},
		},
		RedirectUrls: sdk.RedirectUrls{
			ConfirmUrl:     confirmUrl,
			CancelUrl:      cancelUrl,
			ConfirmUrlType: "CLIENT",
			AppPackageName: options.AppPackageName,
		},
		Options: &sdk.RequestOptions{
			Payment: &sdk.PaymentOptions{Capture: &capture, PayType: "NORMAL"},
			Display: &sdk.DisplayOptions{Locale: options.Locale},
			Extra:   &sdk.ExtraOptions{BranchId: options.BranchId, BranchName: options.BranchName},
		},
	})
	if err != nil {
		return info, wrapError(err)
	}
	if resp.Info.TransactionId <= 0 || resp.Info.PaymentUrl.Web == "" {
		return info, errors.New("linepay: response is missing transactionId or web payment URL")
	}

	return driver.PayLinkInfo{
		PayLink: resp.Info.PaymentUrl.Web,

		ChannelPaymentId: strconv.FormatInt(resp.Info.TransactionId, 10),
	}, nil
}

func (d *Driver) details(ctx context.Context, id int64, paymentId string) (*sdk.PaymentDetails, bool, error) {
	req := sdk.RetrievePaymentDetailsRequest{}
	if id != 0 {
		req.TransactionIds = []int64{id}
	} else {
		req.OrderIds = []string{paymentId}
	}

	resp, err := d.client.RetrievePaymentDetails(ctx, req)
	if notFound(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, wrapError(err)
	}

	for i := range resp.Info {
		info := &resp.Info[i]
		if id != 0 && info.TransactionId != id {
			continue
		}
		if info.TransactionType != "PAYMENT" {
			continue
		}
		if info.TransactionId <= 0 || info.OrderId == "" {
			return nil, false, errors.New("linepay: incomplete payment details")
		}
		if paymentId != "" && info.OrderId != paymentId {
			return nil, false, driver.ErrBadRequest.WithReason("LINE Pay orderId does not match PaymentId")
		}
		return info, true, nil
	}
	return nil, false, nil
}

// QueryPayment only reads the provider's state. An authenticated request remains
// Processing until callback processing confirms it. Poll at intervals of at
// least one second.
func (d *Driver) QueryPayment(ctx context.Context, req driver.QueryPaymentRequest) (info driver.PaymentInfo, ok bool, err error) {
	id, err := paymentReference(req.PaymentId, req.ChannelPaymentId)
	if err != nil {
		return info, false, err
	}

	detail, found, err := d.details(ctx, id, req.PaymentId)
	if err != nil {
		return info, false, err
	}
	if found {
		info, err = parsePayment(detail)
		return info, err == nil, err
	}
	if id == 0 {
		return info, false, nil
	}

	resp, err := d.client.CheckPaymentRequestStatus(ctx, id)
	if notFound(err) {
		return info, false, nil
	}
	if err != nil {
		return info, false, wrapError(err)
	}

	info = driver.PaymentInfo{
		PaymentId:        req.PaymentId,
		ChannelPaymentId: req.ChannelPaymentId,
		ChannelStatus:    resp.ReturnCode,
	}

	switch resp.ReturnCode {
	case sdk.ReturnCodePending:
		info.TaskStatus = driver.TaskStatusProcessing

	case sdk.ReturnCodeCanceled:
		info.TaskStatus = driver.TaskStatusClosed

	case sdk.ReturnCodeFailed:
		info.TaskStatus = driver.TaskStatusFailure
		info.FailReason = resp.ReturnMessage

	case sdk.ReturnCodeAuthorized:
		info.TaskStatus = driver.TaskStatusProcessing

	case sdk.ReturnCodeCompleted:
		detail, found, err := d.details(ctx, id, req.PaymentId)
		if err != nil {
			return info, false, err
		}
		if !found {
			return info, false, errors.New("linepay: completed payment details are not available yet")
		}

		info, err = parsePayment(detail)
		return info, err == nil, err
	}
	return info, true, nil
}

// confirmPayment completes authentication during callback processing using
// trusted order parameters. Concurrent calls recover completed payments from
// authoritative details after a confirmation error.
func (d *Driver) confirmPayment(ctx context.Context, req driver.PaymentOrder) (info driver.PaymentInfo, err error) {
	if req.PaymentId == "" || utf8.RuneCountInString(req.PaymentId) > 100 {
		return info, driver.ErrBadRequest.WithReason("PaymentId must contain 1 to 100 characters")
	}

	id, err := transactionId(req.ChannelPaymentId)
	if err != nil {
		return info, err
	}

	currency := strings.ToUpper(req.PaymentCurrency)
	if !d.metadata.CurrencyIsSupported(currency) {
		return info, driver.ErrUnsupported.WithReasonf("unsupported currency %q", currency)
	}

	amount, err := majorAmount(req.PaymentAmount, currency)
	if err != nil {
		return info, err
	}

	query := driver.QueryPaymentRequest{
		PaymentId:        req.PaymentId,
		ChannelPaymentId: req.ChannelPaymentId,
	}
	info, ok, err := d.QueryPayment(ctx, query)
	if err != nil {
		return info, err
	}
	if !ok {
		return info, driver.ErrUnallowed.WithReason("payment not found")
	}
	if info.TaskStatus == driver.TaskStatusSuccess {
		return info, checkPaidAmount(info, req.PaymentAmount, currency)
	}
	if info.TaskStatus != driver.TaskStatusProcessing || info.ChannelStatus != sdk.ReturnCodeAuthorized {
		return info, driver.ErrUnallowed.WithReason("payment is not awaiting confirmation after authentication")
	}

	confirmed, err := d.client.ConfirmPayment(ctx, id, sdk.ConfirmPaymentRequest{
		Amount:   amount,
		Currency: currency,
	})
	if err != nil {
		// A concurrent call or an earlier timed-out confirmation may have
		// completed. Return success only after checking the captured amount.
		recovered, found, queryErr := d.QueryPayment(ctx, query)
		if queryErr == nil && found && recovered.TaskStatus == driver.TaskStatusSuccess {
			return recovered, checkPaidAmount(recovered, req.PaymentAmount, currency)
		}
		return info, wrapError(err)
	}

	if confirmed.Info.TransactionId != id ||
		confirmed.Info.OrderId != req.PaymentId ||
		confirmed.Info.AuthorizationExpireDate != "" {
		return info, errors.New("linepay: confirmation response does not match the immediate-capture payment")
	}

	paid, err := totalPaid(confirmed.Info.PayInfo, currency)
	if err != nil {
		return info, err
	}
	if paid != req.PaymentAmount {
		return info, errors.New("linepay: confirmed amount does not match payment")
	}

	info.TaskStatus = driver.TaskStatusSuccess
	info.ChannelStatus = "CAPTURE"
	info.PayerPaidAmount = paid
	info.PayerPaidCurrency = currency
	return info, nil
}

func checkPaidAmount(info driver.PaymentInfo, amount int64, currency string) error {
	if info.PayerPaidAmount != amount || info.PayerPaidCurrency != currency {
		return driver.ErrBadRequest.WithReason("PaymentAmount or PaymentCurrency does not match the captured payment")
	}
	return nil
}

func parsePayment(detail *sdk.PaymentDetails) (info driver.PaymentInfo, err error) {
	info = driver.PaymentInfo{
		PaymentId:        detail.OrderId,
		ChannelPaymentId: strconv.FormatInt(detail.TransactionId, 10),
		ChannelStatus:    detail.PayStatus,
		TaskStatus:       driver.TaskStatusUnknown,
	}

	switch detail.PayStatus {
	case "AUTHORIZATION":
		info.TaskStatus = driver.TaskStatusProcessing

	case "EXPIRED_AUTHORIZATION", "VOIDED_AUTHORIZATION":
		info.TaskStatus = driver.TaskStatusClosed

	case "CAPTURE":
		paid, err := totalPaid(detail.PayInfo, detail.Currency)
		if err != nil {
			return info, err
		}
		if paid <= 0 {
			return info, errors.New("linepay: captured payment has no paid amount")
		}
		if detail.Currency != "USD" && detail.Currency != "TWD" && detail.Currency != "THB" {
			return info, errors.New("linepay: missing or unsupported payment currency")
		}
		info.TaskStatus = driver.TaskStatusSuccess
		info.PayerPaidAmount = paid
		info.PayerPaidCurrency = detail.Currency
		info.PayerPaidAt, _ = time.Parse(time.RFC3339, detail.TransactionDate)

		var refunded int64
		for _, refund := range detail.RefundList {
			amount, err := minorAmount(refund.RefundAmount, detail.Currency)
			if err != nil {
				return info, err
			}
			if amount < 0 {
				if amount == -1<<63 {
					return info, errors.New("linepay: invalid refund amount")
				}
				amount = -amount
			}
			if amount > paid-refunded {
				return info, errors.New("linepay: refund total exceeds payment")
			}
			refunded += amount
		}
		info.IsRefunded = refunded >= paid
	}

	return info, nil
}

func (d *Driver) CancelPayment(ctx context.Context, req driver.CancelPaymentRequest) error {
	id, err := paymentReference(req.PaymentId, req.ChannelPaymentId)
	if err != nil {
		return err
	}

	info, ok, err := d.QueryPayment(ctx, driver.QueryPaymentRequest{ //nolint:staticcheck
		PaymentId:        req.PaymentId,
		ChannelPaymentId: req.ChannelPaymentId,
	})
	if err != nil || !ok {
		return err
	}

	switch info.TaskStatus {
	case driver.TaskStatusSuccess:
		return driver.ErrPaid

	case driver.TaskStatusClosed, driver.TaskStatusFailure:
		return nil
	}

	if info.ChannelStatus == "AUTHORIZATION" {
		if id == 0 {
			id, err = transactionId(info.ChannelPaymentId)
			if err != nil {
				return err
			}
		}

		_, err = d.client.Void(ctx, id)
		if err != nil {
			detail, found, queryErr := d.details(ctx, id, req.PaymentId)
			if queryErr == nil && found {
				if detail.PayStatus == "CAPTURE" {
					return driver.ErrPaid
				}
				if detail.PayStatus == "VOIDED_AUTHORIZATION" || detail.PayStatus == "EXPIRED_AUTHORIZATION" {
					return nil
				}
			}
		}

		return wrapError(err)
	}
	return driver.ErrUnsupported.WithReason("LINE Pay cannot actively close an unconfirmed payment request")
}
