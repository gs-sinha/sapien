# Order Service

Order Service owns the lifecycle of a customer order from creation through
delivery or cancellation. It is the first service a request touches: an
order must exist before allocation-service can allocate a rider to it.

## Order lifecycle

An order moves through a small set of statuses:

- `CREATED` - the order exists and has not yet been allocated a rider.
- `ALLOCATED` - allocation-service (see `allocation-service.allocate`) has
  matched a rider to the order.
- `CANCELLED` - the customer or an operator cancelled the order via
  `POST /v1/orders/{orderId}/cancel`.
- `DELIVERED` - the order was delivered. Cancelling a delivered order
  returns `409` with code `ORDER_ALREADY_DELIVERED`.

### Order types

`type` is either `STANDARD` or `QCOM`. QCOM orders may only be allocated a
rider with `qcomSkill=true` - see rider-service's `riders.md` for what that
flag means, and allocation-service's `allocation.md` for the matching rule.

## Listing and filtering

`GET /v1/orders` (`order-service.listOrders`) supports filtering by
`customerId` and `status`, with a `limit` cap on page size (default 50).

## Timeline

`GET /v1/orders/{orderId}/timeline` (`order-service.getOrderTimeline`)
returns the sequence of status events for an order plus an ETA. See
`timeline.md` in this same directory for the polling behaviour clients must
implement.
