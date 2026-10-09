# LINE Pay Payment Driver

[![GoDoc](https://pkg.go.dev/badge/github.com/xgfone/go-payment-driver-linepay)](https://pkg.go.dev/github.com/xgfone/go-payment-driver-linepay)

A Go SDK and [go-payment-driver](https://github.com/xgfone/go-payment-driver)
implementation based on [LINE Pay Online API v4](https://developers-pay.line.me/online-api-v4).
Requires Go 1.23 or later.

- `linepay/` contains a standalone SDK using only the Go standard library, covering all 10 Online API v4 endpoints.
- The root package registers the `linepay_online` driver for one-time payments, queries, automatic confirmation during callback processing, refunds, and refund queries.
- Custom Go identifiers use names such as `Id`, `Url`, and `Http`; JSON fields retain LINE Pay's protocol names.

## Creating a Payment

```go
package example

import (
    "context"
    "encoding/json"
    "os"

    linepay "github.com/xgfone/go-payment-driver-linepay"
    "github.com/xgfone/go-payment-driver/builder"
    "github.com/xgfone/go-payment-driver/driver"
)

func CreatePayment(ctx context.Context) (driver.PayLinkInfo, error) {
    config, err := json.Marshal(linepay.Config{
        ChannelId:     os.Getenv("LINE_PAY_CHANNEL_ID"),
        ChannelSecret: os.Getenv("LINE_PAY_CHANNEL_SECRET"),
        Sandbox:       true,
        Currencies:    []string{"TWD"},
        ReturnUrl:     "https://merchant.example/linepay/return",
        CancelUrl:     "https://merchant.example/linepay/cancel",
    })
    if err != nil {
        return driver.PayLinkInfo{}, err
    }
    d, err := builder.BuildDriver("linepay_online", string(config))
    if err != nil {
        return driver.PayLinkInfo{}, err
    }
    return d.CreatePayment(ctx, driver.CreatePaymentRequest{
        PaymentId:       "order-20261008-001",
        PaymentDesc:     "Example order",
        PaymentCurrency: "TWD",
        PaymentAmount:   10000, // ISO 4217 minor units: TWD 100.00.
    })
}
```

You can also call `linepay.NewDriver(config)` directly. `ChannelId`,
`ChannelSecret`, and `Currencies` are required. The merchant currency allowlist
may contain only `USD`, `TWD`, and `THB`; configured codes are normalized to
uppercase. `Sandbox` defaults to `false`. The production endpoint is
`https://api-pay.line.me`, and the sandbox endpoint is
`https://sandbox-api-pay.line.me`. `Config.Desensitize()` hides the secret.

Persist `PayLinkInfo.ChannelPaymentId` alongside your order.
`ChannelPaymentId` is the original LINE Pay payment transaction Id, represented
as a decimal string, and remains unchanged throughout the payment lifecycle.
The original amount, currency, and business Ids remain in your application's
order records. The driver leaves payment and refund `ChannelData` empty: all
required provider Ids and results already have dedicated interface fields.
`PayLink` is a web payment URL that can be opened directly or encoded as a QR code;
`Metadata.LinkType` is `code_url`.

Driver amounts are integers in ISO 4217 minor units, as required by the payment
interface. The SDK's `amount`, `price`, and `refundAmount` fields use decimal
major units represented by `json.Number`. The driver uses
[go-currency](https://github.com/xgfone/go-currency) v0.3.0:
`FormatMinorToMajor` converts outgoing amounts, and `ParseMajorToMinor` converts
incoming amounts using the actual payment currency's registered precision.
USD, TWD, and THB all have two minor digits, so driver amount `1234` becomes API
amount `12.34`, and `10000` becomes `100.00`. Conversion does not use floating-point
arithmetic, truncate, or round. JSON scientific notation is expanded exactly,
and redundant fractional zeroes are removed before parsing. Unsupported
currencies, excess precision, and int64 overflow produce errors. LINE Pay still
validates merchant and local payment-method precision and amount limits.

LINE Pay sends the authentication result to `redirectUrls.confirmUrl`, which
the driver resolves from `ReturnUrl`. It sets `confirmUrlType=CLIENT`, so the
buyer's browser visits this endpoint after authentication. LINE Pay adds
`orderId` and `transactionId` to identify the payment; the endpoint can be shared
by multiple orders. `CreatePaymentRequest.CallbackUrl` is ignored because this
flow does not require a unique callback URL for each order. Route `ReturnUrl`
through the common `ParseCallbackRequest` method to confirm the payment and
process the result before displaying the final browser page.

`CreatePaymentRequest.ReturnUrl` overrides
`Config.ReturnUrl`. `CancelUrl` independently overrides `Config.CancelUrl`.
Non-empty request URLs are sent unchanged. For an empty request field, the driver
uses its configured default and appends only `PaymentId`, preserving the existing
query string, encoding, and fragment. Default URLs must not already contain the
reserved `PaymentId` parameter. An authentication URL and a cancellation URL are
required; neither acts as a fallback for the other. Each URL must be an absolute
HTTP(S) URL of at most 500 characters. Use HTTPS in production.

`ExtInfo` accepts `linepay.CheckoutOptions`, a pointer to it, or `json.RawMessage`:

```go
req.ExtInfo = linepay.CheckoutOptions{
    Locale: "zh_TW",
    AppPackageName: "com.example.shop",
    BranchId: "branch-1",
    BranchName: "Example store",
}
```

The driver creates one package containing one product, uses `PaymentDesc` as the
product name (falling back to `PaymentId` when empty), and sets `capture=true`
and `payType=NORMAL`. Use the SDK directly for more complex packages,
pre-approved payments, or separate authorization and capture.

## Completing a Payment

The application uses only the common `driver.Driver` interface. Payment
confirmation is an internal step of `ParseCallbackRequest`; there is no
provider-specific confirmation method for the application to call.

1. Create a payment and persist its `ChannelPaymentId` alongside the original order.
2. Route `ReturnUrl`, the authentication redirect endpoint, to your common callback handler.
3. In every callback request, supply a common `LoadPayment` function backed by trusted order storage. The application does not decide whether confirmation is needed.
4. The LINE Pay driver checks the official request status. Once authentication is complete (`0110`), it loads the order, verifies the original transaction Id, and calls the confirmation API using the stored amount and currency.
5. The driver returns the common payment result. Persist successful results idempotently and send the callback response.

```go
// d is a driver.Driver built through the common builder.
// loadPayment has this public callback signature and reads your order storage:
// driver.PaymentLoader: func(context.Context, driver.PaymentReference)
// (driver.PaymentOrder, bool, error).
callback, err := d.ParseCallbackRequest(ctx, driver.CallbackRequest{
    Request:     r,
    LoadPayment: loadPayment,
})
// Check err and callback.PaymentInfo.TaskStatus, then persist the result.
d.SendCallbackResponse(ctx, w, err)
```

`LoadPayment` receives a `driver.PaymentReference` containing both the callback's
`PaymentId` and `ChannelPaymentId`. Scope the lookup to the current payment
channel account and require both Ids to match the same stored order. It returns
`driver.PaymentOrder` with the original `PaymentId`,
`ChannelPaymentId`, `PaymentAmount` in minor units, and `PaymentCurrency`.
The common callback handler can always supply this function. Drivers that only
read completed callback results may ignore it; the LINE Pay driver requires a
non-nil loader on every callback and decides when to invoke it. A nil loader
returns an error immediately, before parsing the request or calling the API.
Order fields must come from server-side storage. Browser-supplied
amounts, currencies, and status flags are ignored. Missing orders, invalid order
parameters, or mismatched transaction Ids cannot trigger confirmation.

A LINE Pay authentication callback is a GET request containing `orderId` and
`transactionId`. It is not a signed JSON event like a Stripe webhook, and the
request itself does not establish payment success. The driver queries the
official API before proceeding. See
[Redirection pages](https://developers-pay.line.me/online-api-v4/merchant/redirection-pages)
and [Confirm payment](https://developers-pay.line.me/online-api-v4/confirm-payment).
No refund callback events are provided in this flow; refund callback types return
`ErrUnsupported`, and `CreateRefundRequest.CallbackUrl` is ignored.

`QueryPayment` remains read-only and accepts only the common payment Id fields.
It does not need an amount or currency and never calls the confirmation API.
An authenticated request remains Processing until its callback is processed.
Poll at intervals of at least one second. With only a merchant order Id and no
original transaction Id, LINE Pay can retrieve only payments already authorized
or captured; it cannot recover an unconfirmed payment request.

The SDK's default HTTP timeout is 70 seconds. A shorter caller context deadline
still takes precedence. LINE Pay recommends at least a 40-second read timeout
for confirmation; allow enough time for callback processing to finish.

`SendCallbackResponse` sends HTTP 200 when there is no error and HTTP 500 when
processing fails. Your callback endpoint is responsible for the final browser
page or navigation. LINE Pay does not promise repeated redirect delivery in the
same way as a webhook. A missing callback leaves the payment unconfirmed;
read-only polling does not replace callback processing.

Repeated callbacks return the existing captured payment without confirming it
again. If concurrent callbacks or a network failure produce a confirmation
error, the driver checks authoritative payment details and verifies the captured
amount before recovering success. The SDK does not automatically retry POST
requests. LINE Pay does not expose a payment-request expiry setting used by this
driver, so `ExpiresIn` is ignored and LINE Pay's own deadline applies. `Share=true`
returns `ErrUnsupported` because this driver does not support profit sharing.

## Statuses and Cancellation

| LINE Pay status                                            | Driver behavior/status                                    |
| ---------------------------------------------------------- | --------------------------------------------------------- |
| check `0000`, awaiting authentication                      | Processing                                                |
| check `0110`, authenticated                                | Processing; callback processing confirms the payment      |
| check `0121`, canceled or authentication timed out         | Closed                                                    |
| check `0122`, payment failed                               | Failure                                                   |
| check `0123`, payment completed                            | Retrieves details to verify status and actual paid amount |
| `payStatus=AUTHORIZATION`                                  | Processing                                                |
| `payStatus=CAPTURE`                                        | Success                                                   |
| `payStatus=VOIDED_AUTHORIZATION` / `EXPIRED_AUTHORIZATION` | Closed                                                    |
| Unrecognized payment-detail status                         | Unknown                                                   |

`CancelPayment` never confirms a payment. It returns `ErrPaid` for a completed
payment, and nil for a closed, failed, or nonexistent payment. An authorized
payment that has not been captured can be canceled using
[void](https://developers-pay.line.me/online-api-v4/void). LINE Pay has no endpoint
to actively close an unconfirmed payment request, so that case returns
`ErrUnsupported`. Visiting `CancelUrl` alone must not be treated as proof that
an order has closed; query the official state.

Captured payment details populate `PayerPaidAt` from `transactionDate` when
available. Confirmation responses do not provide a completion timestamp, so a
newly confirmed result leaves that field zero-valued; a later query can populate
it. Do not overwrite a saved timestamp with a zero value. LINE Pay does not
provide a usable payer Id, so `PayerId` remains empty. `PayerPaidAmount` includes
all payment components, such as credit cards, balances, and points. `IsRefunded`
is true only when cumulative refunds equal the original paid amount; partial
refunds leave it false.

## Refunds

```go
refund, err := d.RefundPayment(ctx, driver.CreateRefundRequest{
    PaymentId:        storedPaymentId,
    ChannelPaymentId: storedChannelPaymentId,
    PaymentAmount:    10000,
    PaymentCurrency:  "TWD",
    RefundId:         "refund-20261008-001",
    RefundAmount:     2000, // TWD 20.00.
    RefundReason:     "Customer request",
})
// On success, persist refund.ChannelRefundId alongside your refund task.

result, ok, err := d.QueryRefund(ctx, driver.QueryRefundRequest{
    PaymentId:        storedPaymentId,
    ChannelPaymentId: storedChannelPaymentId,
    RefundId:         refund.RefundId,
    ChannelRefundId:  refund.ChannelRefundId,
})
```

Before refunding, the driver verifies the actual payment state, currency, and
total paid amount. A successful [refund API](https://developers-pay.line.me/online-api-v4/refund)
response returns synchronous Success. `RefundedAt` comes from the official
`refundTransactionDate`. Fully refunded payments, insufficient balance, and
refund period/state/amount restrictions map to `ErrPaymentRefundedFully`,
`ErrBalanceInsufficient`, and `ErrUnallowed`, respectively.

**LINE Pay does not accept a merchant `RefundId` or a refund idempotency key.**
`RefundPayment` echoes `RefundId` and `RefundReason` in their dedicated result
fields; retain them in your application records. `QueryRefund` cannot recover
the business refund reason from LINE Pay and leaves `RefundReason` empty. Your
application must serialize refund attempts and manage local tasks idempotently.
Do not blindly retry the same refund: if the first request succeeded, another
request creates another refund. After a timeout or lost response, reconcile the
original payment's `refundList` before proceeding. The driver cannot match a
refund automatically using only `RefundId`.

`QueryRefund` requires `ChannelRefundId`. Providing only a business refund Id
returns `ErrUnsupported`. Queries for nonexistent payments or refunds return
`ok=false, err=nil`.

## SDK

See [linepay/README.md](linepay/README.md) for SDK usage and its endpoint list.
The SDK also exposes pre-approved payments, separate capture, and void; the root
driver provides one-time payments with automatic capture.

## Verification

Callback completion requires the updated public `CallbackRequest.LoadPayment`,
`PaymentLoader`, `PaymentReference`, and `PaymentOrder` definitions in
`go-payment-driver`. The published dependency v0.10.4 does not contain them.
During development, `go.mod` uses the local sibling checkout through this
replacement:

```go
replace github.com/xgfone/go-payment-driver => ../../../github.com/xgfone/go-payment-driver
```

After the common library is released, update the dependency version and remove
the local replacement before publishing this driver.

```sh
go test -race ./...
go vet ./...
```

Tests use local HTTP mock servers and cover signatures against transmitted
bytes, all endpoints, status codes, currency conversion and exact amounts,
transaction Ids, redirects, callback confirmation and concurrent recovery,
partial/full refunds, redirect verification, and invalid data. Merchant
credentials are not required. Real sandbox integration requires your own
channel credentials and a LINE Pay test account to complete authentication.
