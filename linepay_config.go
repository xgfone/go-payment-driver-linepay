// Copyright 2026 xgfone
// SPDX-License-Identifier: Apache-2.0

// Package linepay implements LINE Pay Online payments for go-payment-driver.
package linepay

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"unicode/utf8"
)

const Type = "linepay"

type Config struct {
	ChannelId     string
	ChannelSecret string

	// Merchant allowlist, restricted to USD, TWD and THB.
	Currencies              []string
	MerchantDeviceProfileId string
	MerchantDeviceType      string

	// Default browser URLs. PaymentId is appended only to defaults.
	// The return handler must process the authentication redirect through
	// ParseCallbackRequest to complete the payment.
	ReturnUrl string
	CancelUrl string

	Sandbox bool
}

func (c *Config) Init() error {
	if strings.TrimSpace(c.ChannelId) == "" {
		return errors.New("linepay: missing ChannelId")
	}
	if strings.TrimSpace(c.ChannelSecret) == "" {
		return errors.New("linepay: missing ChannelSecret")
	}
	if len(c.Currencies) == 0 {
		return errors.New("linepay: missing Currencies")
	}

	c.Currencies = slices.Clone(c.Currencies)
	for i, currency := range c.Currencies {
		currency = strings.ToUpper(strings.TrimSpace(currency))
		switch currency {
		case "USD", "TWD", "THB":
			c.Currencies[i] = currency

		default:
			return fmt.Errorf("linepay: unsupported currency %q", currency)
		}
	}

	if err := validateUrl("ReturnUrl", c.ReturnUrl); err != nil {
		return err
	}
	return validateUrl("CancelUrl", c.CancelUrl)
}

func (c *Config) Desensitize() {
	c.ChannelSecret = "***"
}

func validateUrl(name, value string) error {
	if value == "" {
		return nil
	}

	u, err := url.Parse(value)
	if err != nil || u.Host == "" || u.User != nil ||
		(u.Scheme != "http" && u.Scheme != "https") ||
		utf8.RuneCountInString(value) > 500 {
		return fmt.Errorf("%s must be an absolute HTTP(S) URL of at most 500 characters", name)
	}

	return nil
}

func returnUrl(requestUrl, defaultUrl, paymentId string) string {
	if requestUrl != "" {
		return requestUrl
	}
	if defaultUrl == "" {
		return ""
	}

	base, fragment, hasFragment := strings.Cut(defaultUrl, "#")
	sep := "?"
	if _, query, ok := strings.Cut(base, "?"); ok {
		sep = "&"
		if query == "" || strings.HasSuffix(query, "&") {
			sep = ""
		}
	}

	base += sep + "PaymentId=" + url.QueryEscape(paymentId)
	if hasFragment {
		base += "#" + fragment
	}

	return base
}

// CheckoutOptions may be supplied as ExtInfo as a value, pointer, or json.RawMessage.
type CheckoutOptions struct {
	Locale         string
	BranchId       string
	BranchName     string
	AppPackageName string
}

func checkoutOptions(ext any) (options CheckoutOptions, err error) {
	switch v := ext.(type) {
	case nil:
	case CheckoutOptions:
		options = v

	case *CheckoutOptions:
		if v != nil {
			options = *v
		}

	case json.RawMessage:
		if len(v) != 0 {
			err = json.Unmarshal(v, &options)
		}

	default:
		err = fmt.Errorf("unsupported ExtInfo type %T", ext)
	}
	return
}
