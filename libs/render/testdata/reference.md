# Grocery Restocking — Product Requirements

> Households choose how often they buy items, and orders are created for them automatically.

## Who it is for

- **Household**: a family that buys groceries every week or month.
- **Store admin**: keeps products and supplier prices up to date.
  - prices change weekly
  - suppliers are listed per product

## Features

### Recurring orders

Households choose how often they want an item, and an order is created *automatically* each time. See the [payments feature](#pay-with-a-local-method) and <https://example.com/docs>.

1. An item can repeat every 1 to 8 weeks.
2. Skipping the next order takes one tap.
3. When payment fails, retry 3 times over 3 days, then pause the order.

Done when:

- [x] A household sets Milk to repeat weekly and an order appears the next week.
- [ ] A failed payment retries and then pauses the order.

## Data

| Thing | Field | Type |
| --- | --- | --- |
| Order | status | one of waiting, paid, delivered |
| Order line | quantity | number |
| Product | price | money |

## Notes

Use `kobo` for money. ~~Decimals~~ are not allowed.

```go
type Money int64 // kobo
```

---

<script>alert(1)</script>

Some text with <b>raw html</b> and an image ![the logo](https://example.com/logo.png).
