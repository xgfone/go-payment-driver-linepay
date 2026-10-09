// Copyright 2026 xgfone
// SPDX-License-Identifier: Apache-2.0

package linepay

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/xgfone/go-payment-driver/driver"

	sdk "github.com/xgfone/go-payment-driver-linepay/linepay"
)

func (d *Driver) RefundPayment(ctx context.Context, req driver.CreateRefundRequest) (info driver.RefundInfo, err error) {
	if req.PaymentId == "" || req.RefundId == "" {
		return info, driver.ErrBadRequest.WithReason("PaymentId and RefundId are required")
	}
	if req.PaymentAmount <= 0 || req.RefundAmount <= 0 || req.RefundAmount > req.PaymentAmount {
		return info, driver.ErrBadRequest.WithReason("refund amount must be positive and no greater than PaymentAmount")
	}

	currency := strings.ToUpper(req.PaymentCurrency)
	if !d.metadata.CurrencyIsSupported(currency) {
		return info, driver.ErrUnsupported.WithReasonf("unsupported currency %q", currency)
	}

	id, err := paymentReference(req.PaymentId, req.ChannelPaymentId)
	if err != nil {
		return info, err
	}

	detail, found, err := d.details(ctx, id, req.PaymentId)
	if err != nil {
		return info, err
	}
	if !found {
		return info, driver.ErrUnallowed.WithReason("payment not found or not confirmed")
	}

	payment, err := parsePayment(detail)
	if err != nil {
		return info, err
	}
	if payment.TaskStatus != driver.TaskStatusSuccess {
		return info, driver.ErrUnallowed.WithReason("payment has not been captured")
	}
	if payment.IsRefunded {
		return info, driver.ErrPaymentRefundedFully
	}
	if err := checkPaidAmount(payment, req.PaymentAmount, currency); err != nil {
		return info, err
	}

	amount, err := majorAmount(req.RefundAmount, currency)
	if err != nil {
		return info, err
	}

	resp, err := d.client.Refund(ctx, detail.TransactionId, sdk.RefundRequest{RefundAmount: amount})
	if err != nil {
		return info, wrapError(err)
	}
	if resp.Info.RefundTransactionId <= 0 {
		return info, errors.New("linepay: refund response is missing refundTransactionId; reconcile before retrying")
	}

	refundId := strconv.FormatInt(resp.Info.RefundTransactionId, 10)
	info = driver.RefundInfo{
		PaymentId: req.PaymentId,

		RefundId:     req.RefundId,
		RefundReason: req.RefundReason,

		ChannelRefundId: refundId,
		ChannelStatus:   resp.ReturnCode,

		TaskStatus: driver.TaskStatusSuccess,
	}

	info.RefundedAt, _ = time.Parse(time.RFC3339, resp.Info.RefundTransactionDate)
	return info, nil
}

// QueryRefund requires the provider's refund transaction Id. LINE Pay has no
// merchant refund Id field, so an ambiguous creation cannot be recovered by
// RefundId alone and must be reconciled against payment details/refundList.
func (d *Driver) QueryRefund(ctx context.Context, req driver.QueryRefundRequest) (info driver.RefundInfo, ok bool, err error) {
	if req.ChannelRefundId == "" {
		return info, false, driver.ErrUnsupported.WithReason("LINE Pay refund query requires ChannelRefundId")
	}

	refundId, err := transactionId(req.ChannelRefundId)
	if err != nil {
		return info, false, err
	}

	var paymentId int64
	if req.ChannelPaymentId != "" {
		paymentId, err = transactionId(req.ChannelPaymentId)
		if err != nil {
			return info, false, err
		}
	}

	resp, err := d.client.RetrievePaymentDetails(ctx, sdk.RetrievePaymentDetailsRequest{
		TransactionIds: []int64{refundId},
	})
	if err != nil && !notFound(err) {
		return info, false, wrapError(err)
	}
	if err == nil {
		for _, detail := range resp.Info {
			if detail.TransactionId != refundId ||
				(detail.TransactionType != "PARTIAL_REFUND" && detail.TransactionType != "PAYMENT_REFUND") {
				continue
			}
			if req.PaymentId != "" && req.PaymentId != detail.OrderId {
				return info, false, driver.ErrBadRequest.WithReason("refund orderId does not match PaymentId")
			}
			if paymentId != 0 && detail.OriginalTransactionId != paymentId {
				return info, false, driver.ErrBadRequest.WithReason("refund originalTransactionId does not match ChannelPaymentId")
			}
			if detail.OrderId == "" {
				return info, false, errors.New("linepay: refund details are missing orderId")
			}

			return refundInfo(detail.OrderId, req.RefundId, req.ChannelRefundId, detail.TransactionType, detail.TransactionDate), true, nil
		}
	}

	// Some responses expose the refund only in the original payment's list.
	if paymentId == 0 && req.PaymentId == "" {
		return info, false, nil
	}

	detail, found, err := d.details(ctx, paymentId, req.PaymentId)
	if err != nil || !found {
		return info, false, err
	}

	for _, refund := range detail.RefundList {
		if refund.RefundTransactionId == refundId &&
			(refund.TransactionType == "PARTIAL_REFUND" || refund.TransactionType == "PAYMENT_REFUND") {
			return refundInfo(detail.OrderId, req.RefundId, req.ChannelRefundId, refund.TransactionType, refund.RefundTransactionDate), true, nil
		}
	}

	return info, false, nil
}

func refundInfo(paymentId, refundId, channelRefundId, status, date string) driver.RefundInfo {
	refundedAt, _ := time.Parse(time.RFC3339, date)
	return driver.RefundInfo{
		PaymentId: paymentId,

		RefundId:   refundId,
		RefundedAt: refundedAt,

		ChannelRefundId: channelRefundId,
		ChannelStatus:   status,

		TaskStatus: driver.TaskStatusSuccess,
	}
}
