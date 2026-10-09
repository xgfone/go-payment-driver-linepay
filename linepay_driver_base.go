// Copyright 2026 xgfone
// SPDX-License-Identifier: Apache-2.0

package linepay

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/big"
	"slices"
	"strconv"
	"strings"

	"github.com/xgfone/go-currency"
	"github.com/xgfone/go-payment-driver/builder"
	"github.com/xgfone/go-payment-driver/driver"

	sdk "github.com/xgfone/go-payment-driver-linepay/linepay"
)

func init() {
	metadata := driver.NewMetadata(Type, "online").
		WithLinkType(driver.LinkTypeCodeUrl).
		WithCurrencies("USD", "TWD", "THB")
	builder.Register(builder.New(func(b builder.Builder, c Config) (driver.Driver, error) {
		return newDriver(b, c)
	}, metadata))
}

// NewDriver builds the same driver as builder.BuildDriver("linepay_online", ...).
func NewDriver(config Config) (*Driver, error) {
	return newDriver(builder.Get("linepay_online"), config)
}

func newDriver(b builder.Builder, config Config) (*Driver, error) {
	if err := config.Init(); err != nil {
		return nil, err
	}

	client, err := sdk.NewClient(sdk.Config{
		ChannelId:     config.ChannelId,
		ChannelSecret: config.ChannelSecret,

		MerchantDeviceProfileId: config.MerchantDeviceProfileId,
		MerchantDeviceType:      config.MerchantDeviceType,

		Sandbox: config.Sandbox,
	})
	if err != nil {
		return nil, err
	}

	metadata := b.Metadata()
	metadata.Currencies = slices.Clone(config.Currencies)
	return &Driver{config: config, metadata: metadata, client: client}, nil
}

type Driver struct {
	config   Config
	metadata driver.Metadata
	client   *sdk.Client
}

type OnlineDriver = Driver

var _ driver.Driver = (*Driver)(nil)

func (d *Driver) Metadata() driver.Metadata {
	metadata := d.metadata
	metadata.Currencies = slices.Clone(metadata.Currencies)
	metadata.Channels = slices.Clone(metadata.Channels)
	return metadata
}

// LINE Pay receives major units. Use the currency registry's precision for
// conversion in both directions, without floating-point arithmetic.
func majorAmount(amount int64, currencyCode string) (json.Number, error) {
	if amount <= 0 {
		return "", driver.ErrBadRequest.WithReason("amount must be positive")
	}

	major, err := currency.FormatMinorToMajor(amount, currencyCode)
	if err != nil {
		return "", driver.ErrBadRequest.WithError(err)
	}

	return json.Number(major), nil
}

func minorAmount(amount json.Number, currencyCode string) (int64, error) {
	if amount == "" {
		return 0, errors.New("linepay: missing amount in response")
	}

	// Bound exponent/size before parsing untrusted decimal numbers.
	if len(amount) > 64 {
		return 0, errors.New("linepay: amount is too large")
	}
	if !json.Valid([]byte(amount)) {
		return 0, fmt.Errorf("linepay: invalid amount %q", amount)
	}

	major := amount.String()
	if index := strings.IndexAny(major, "eE"); index >= 0 {
		exponent, err := strconv.ParseInt(amount.String()[index+1:], 10, 64)
		if err != nil || exponent < -64 || exponent > 64 {
			return 0, fmt.Errorf("linepay: amount exponent is out of range: %q", amount)
		}

		// go-currency accepts decimal notation. Expand JSON exponents exactly;
		// the 64-character mantissa and bounded exponent need at most 128
		// fractional digits. This normalization does not scale currency units.
		decimal, ok := new(big.Rat).SetString(major)
		if !ok {
			return 0, fmt.Errorf("linepay: invalid amount %q", amount)
		}
		major = decimal.FloatString(128)
	}

	if strings.Contains(major, ".") {
		major = strings.TrimRight(strings.TrimRight(major, "0"), ".")
	}

	minor, err := currency.ParseMajorToMinor(major, currencyCode)
	if err != nil {
		return 0, fmt.Errorf("linepay: amount %q cannot be represented in %s minor units: %w", amount, currencyCode, err)
	}

	return minor, nil
}

func totalPaid(payInfo []sdk.PayInfo, currencyCode string) (int64, error) {
	if len(payInfo) == 0 {
		return 0, errors.New("linepay: response is missing payInfo")
	}

	var total int64
	for _, info := range payInfo {
		amount, err := minorAmount(info.Amount, currencyCode)
		if err != nil {
			return 0, err
		}
		if amount < 0 || total > math.MaxInt64-amount {
			return 0, errors.New("linepay: invalid payment amount")
		}
		total += amount
	}
	return total, nil
}

func transactionId(value string) (int64, error) {
	id, err := strconv.ParseInt(value, 10, 64)
	if err != nil || id <= 0 || strconv.FormatInt(id, 10) != value {
		return 0, driver.ErrBadRequest.WithReason("invalid ChannelPaymentId or transactionId")
	}
	return id, nil
}

func paymentReference(paymentId, channelPaymentId string) (int64, error) {
	if channelPaymentId != "" {
		return transactionId(channelPaymentId)
	}

	if paymentId == "" {
		return 0, driver.ErrBadRequest.WithReason("PaymentId or ChannelPaymentId is required")
	}

	return 0, nil
}

func notFound(err error) bool {
	var e *sdk.Error
	return errors.As(err, &e) && (e.ReturnCode == "1150" || e.ReturnCode == "1159")
}

func wrapError(err error) error {
	var e *sdk.Error
	if !errors.As(err, &e) {
		return err
	}

	switch e.ReturnCode {
	case "1165":
		return driver.ErrPaymentRefundedFully

	case "1142", "2042":
		return driver.ErrBalanceInsufficient.WithReason(e.Error())

	case "1183":
		return driver.ErrTooSmallPaymentAmount

	case "1163", "1164", "1179":
		return driver.ErrUnallowed.WithReason(e.Error())

	case "1124", "1153", "1155", "1172", "1178", "1184", "2101", "2102":
		return driver.ErrBadRequest.WithReason(e.Error())

	default:
		return err
	}
}
