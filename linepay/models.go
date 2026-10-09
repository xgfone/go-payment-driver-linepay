// Copyright 2026 xgfone
// SPDX-License-Identifier: Apache-2.0

package linepay

import "encoding/json"

type RequestPaymentRequest struct {
	Amount       json.Number     `json:"amount"`
	Currency     string          `json:"currency"`
	OrderId      string          `json:"orderId"`
	Packages     []Package       `json:"packages"`
	RedirectUrls RedirectUrls    `json:"redirectUrls"`
	Options      *RequestOptions `json:"options,omitempty"`
}

type Package struct {
	Id       string      `json:"id"`
	Name     string      `json:"name,omitempty"`
	Amount   json.Number `json:"amount"`
	Products []Product   `json:"products,omitempty"`
}

type Product struct {
	Id       string `json:"id,omitempty"`
	Name     string `json:"name"`
	ImageUrl string `json:"imageUrl,omitempty"`
	Quantity int64  `json:"quantity"`

	Price         json.Number `json:"price"`
	OriginalPrice json.Number `json:"originalPrice,omitempty"`
}

type RedirectUrls struct {
	ConfirmUrl     string `json:"confirmUrl,omitempty"`
	CancelUrl      string `json:"cancelUrl,omitempty"`
	ConfirmUrlType string `json:"confirmUrlType,omitempty"`
	AppPackageName string `json:"appPackageName,omitempty"`
}

type RequestOptions struct {
	Display       *DisplayOptions `json:"display,omitempty"`
	Payment       *PaymentOptions `json:"payment,omitempty"`
	Extra         *ExtraOptions   `json:"extra,omitempty"`
	Events        []Event         `json:"events,omitempty"`
	RegPayRequest *RegPayRequest  `json:"regPayRequest,omitempty"`
}

type DisplayOptions struct {
	Locale                 string `json:"locale,omitempty"`
	CheckConfirmUrlBrowser *bool  `json:"checkConfirmUrlBrowser,omitempty"`
}

type PaymentOptions struct {
	Capture *bool  `json:"capture,omitempty"`
	PayType string `json:"payType,omitempty"`
}

type ExtraOptions struct {
	BranchId             string                `json:"branchId,omitempty"`
	BranchName           string                `json:"branchName,omitempty"`
	PromotionRestriction *PromotionRestriction `json:"promotionRestriction,omitempty"`
}

type PromotionRestriction struct {
	RewardLimit json.Number `json:"rewardLimit"`
	UseLimit    json.Number `json:"useLimit,omitempty"`
}

type Event struct {
	Code            string      `json:"code,omitempty"`
	TotalAmount     json.Number `json:"totalAmount,omitempty"`
	ProductQuantity int64       `json:"productQuantity,omitempty"`
}

type RegPayRequest struct {
	PerTransactionLimit json.Number `json:"perTransactionLimit,omitempty"`
	ProductPrice        json.Number `json:"productPrice,omitempty"`
	RecurringMonth      int         `json:"recurringMonth,omitempty"`
	RecurringDay        int         `json:"recurringDay,omitempty"`
	RecurringDayOfWeek  string      `json:"recurringDayOfWeek,omitempty"`
	RecurringPeriod     string      `json:"recurringPeriod,omitempty"`
	RegPayPeriodType    string      `json:"regPayPeriodType,omitempty"`
}

type PaymentUrl struct {
	Web       string `json:"web"`
	App       string `json:"app"`
	Universal string `json:"universal"`
}

type RequestPaymentInfo struct {
	TransactionId      int64      `json:"transactionId"`
	PaymentUrl         PaymentUrl `json:"paymentUrl"`
	PaymentAccessToken string     `json:"paymentAccessToken"`
}

type ConfirmPaymentRequest struct {
	Amount   json.Number `json:"amount"`
	Currency string      `json:"currency"`
}

type CaptureRequest = ConfirmPaymentRequest

type ConfirmPaymentInfo struct {
	TransactionId           int64              `json:"transactionId"`
	OrderId                 string             `json:"orderId"`
	PayInfo                 []PayInfo          `json:"payInfo"`
	Packages                []Package          `json:"packages"`
	PaymentProvider         string             `json:"paymentProvider"`
	AuthorizationExpireDate string             `json:"authorizationExpireDate"`
	RegKey                  string             `json:"regKey"`
	MerchantReference       *MerchantReference `json:"merchantReference"`
}

type MerchantReference struct {
	AffiliateCards []AffiliateCard `json:"affiliateCards"`
}

type AffiliateCard struct {
	CardId   string `json:"cardId"`
	CardType string `json:"cardType"`
}

type PayInfo struct {
	Amount                 json.Number `json:"amount"`
	Method                 string      `json:"method"`
	CreditCardBrand        string      `json:"creditCardBrand,omitempty"`
	CreditCardNickname     string      `json:"creditCardNickname,omitempty"`
	MaskedCreditCardNumber string      `json:"maskedCreditCardNumber,omitempty"`
}

type RetrievePaymentDetailsRequest struct {
	OrderIds       []string
	TransactionIds []int64
}

type PaymentDetails struct {
	TransactionId           int64           `json:"transactionId"`
	OriginalTransactionId   int64           `json:"originalTransactionId"`
	TransactionDate         string          `json:"transactionDate"`
	TransactionType         string          `json:"transactionType"`
	OrderId                 string          `json:"orderId"`
	Currency                string          `json:"currency"`
	PayStatus               string          `json:"payStatus"`
	PayInfo                 []PayInfo       `json:"payInfo"`
	Packages                []Package       `json:"packages"`
	RefundList              []RefundDetails `json:"refundList"`
	ProductName             string          `json:"productName"`
	MerchantName            string          `json:"merchantName"`
	PaymentProvider         string          `json:"paymentProvider"`
	AuthorizationExpireDate string          `json:"authorizationExpireDate"`
}

type RefundRequest struct {
	// Omit for a full refund; otherwise use decimal major currency units.
	RefundAmount json.Number `json:"refundAmount,omitempty"`
}

type RefundInfo struct {
	RefundTransactionId   int64  `json:"refundTransactionId"`
	RefundTransactionDate string `json:"refundTransactionDate"`
}

type RefundDetails struct {
	RefundTransactionId   int64       `json:"refundTransactionId"`
	RefundTransactionDate string      `json:"refundTransactionDate"`
	RefundAmount          json.Number `json:"refundAmount"`
	TransactionType       string      `json:"transactionType"`
}

type RequestPreapprovedPaymentRequest struct {
	ProductName string      `json:"productName"`
	Amount      json.Number `json:"amount"`
	Currency    string      `json:"currency"`
	OrderId     string      `json:"orderId"`
	Capture     *bool       `json:"capture,omitempty"`
}

type PreapprovedPaymentInfo struct {
	TransactionId           int64  `json:"transactionId"`
	OrderId                 string `json:"orderId"`
	TransactionDate         string `json:"transactionDate"`
	PaymentProvider         string `json:"paymentProvider"`
	AuthorizationExpireDate string `json:"authorizationExpireDate"`
}
