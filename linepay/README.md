# LINE Pay Online API v4 Go SDK

This SDK uses only the Go standard library. API definitions follow the
[official documentation](https://developers-pay.line.me/online-api-v4).
Go fields use names such as `Id`, `Url`, and `Http`; JSON fields retain the
official protocol names and casing.

```go
package example

import (
    "context"
    "encoding/json"

    "github.com/xgfone/go-payment-driver-linepay/linepay"
)

func Request(ctx context.Context, channelId, channelSecret string) (*linepay.Response[linepay.RequestPaymentInfo], error) {
    client, err := linepay.NewClient(linepay.Config{
        ChannelId: channelId,
        ChannelSecret: channelSecret,
        Sandbox: true,
    })
    if err != nil {
        return nil, err
    }
    return client.RequestPayment(ctx, linepay.RequestPaymentRequest{
        Amount: json.Number("100"), // Major units: TWD 100; use "12.34" for USD 12.34.
        Currency: "TWD",
        OrderId: "order-20261008-001",
        Packages: []linepay.Package{{
            Id: "1",
            Amount: "100",
            Products: []linepay.Product{{
                Name: "Example product", Quantity: 1, Price: "100",
            }},
        }},
        RedirectUrls: linepay.RedirectUrls{
            ConfirmUrl: "https://merchant.example/confirm",
            CancelUrl: "https://merchant.example/cancel",
        },
    })
}
```

After creation, persist `Info.TransactionId`. Once the buyer has authenticated,
confirm the payment:

```go
confirmed, err := client.ConfirmPayment(ctx, transactionId, linepay.ConfirmPaymentRequest{
    Amount: "100", Currency: "TWD",
})
```

Amount fields use `json.Number` and are encoded as JSON numbers. Avoid converting
amounts to `float64` first. The SDK receives amounts directly in major units and
does not validate package totals or local payment limits. The root driver uses
[go-currency](https://github.com/xgfone/go-currency) to convert between its minor-unit
interface and the SDK's major-unit amounts; that dependency is not used by the
SDK package itself.

Transaction Ids use `int64` to retain exact 19-digit values. Convert them to
decimal strings when passing them to JavaScript or other systems that cannot
represent those integers precisely.

| Method                           | HTTP endpoint                                            |
| -------------------------------- | -------------------------------------------------------- |
| RequestPayment                   | POST /v4/payments/request                                |
| CheckPaymentRequestStatus        | GET /v4/payments/requests/{transactionId}/check          |
| ConfirmPayment                   | POST /v4/payments/{transactionId}/confirm                |
| Capture                          | POST /v4/payments/authorizations/{transactionId}/capture |
| Void                             | POST /v4/payments/authorizations/{transactionId}/void    |
| RetrievePaymentDetails           | GET /v4/payments                                         |
| Refund                           | POST /v4/payments/{transactionId}/refund                 |
| CheckPreapprovedPaymentKeyStatus | GET /v4/payments/preapprovedPay/{regKey}/check           |
| RequestPreapprovedPayment        | POST /v4/payments/preapprovedPay/{regKey}/payment        |
| DiscardPreapprovedPaymentKey     | POST /v4/payments/preapprovedPay/{regKey}/expire         |

`RetrievePaymentDetailsRequest` accepts multiple `OrderIds` or `TransactionIds`,
encoded as repeated `orderId` or `transactionId` query parameters.
The `creditCardAuth` argument of `CheckPreapprovedPaymentKeyStatus` is a `*bool`:
nil omits it, while a pointer to false explicitly sends false.
An empty `RefundRequest.RefundAmount` requests a full refund.
`PaymentOptions.Capture` is a `*bool`; a pointer to false explicitly selects
separate authorization and capture. The default is automatic capture.

The client signs requests with HMAC-SHA256 and Base64 according to the
[official authentication rules](https://developers-pay.line.me/online/prerequisites).
The signed data exactly matches the transmitted body or query string, and every
request uses a fresh UUID v4 nonce. GET signatures exclude the question mark.
POST endpoints without a body send an empty body. The channel secret is never
sent as an HTTP header.

All methods accept a context. The default timeout is 70 seconds; customize the
transport and timeout with `Config.HttpClient`. `Config.BaseUrl` can override the
service origin for local testing. The client does not follow redirects and
limits response bodies to 4 MiB. Clients support concurrent calls after
construction; do not concurrently modify the supplied transport configuration.

Normal API methods accept only `returnCode=0000`. `CheckPaymentRequestStatus`
also treats `0110`, `0121`, `0122`, and `0123` as normal statuses; callers must
inspect `ReturnCode`. An API failure returns `*linepay.Error` and retains the
Response. Use `errors.As` to inspect `ReturnCode` and `ReturnMessage`.
Non-200 HTTP responses return `*linepay.HttpError`. Transport and decoding
errors preserve their causes. A response without `returnCode` is not success.

The SDK does not automatically retry requests. Refunds have no business refund
Id or idempotency key, so reconcile the result after a network failure before
retrying. A LINE Pay redirect carries no signed payment-success proof: confirm
using trusted server-side parameters and update orders from official API results.
