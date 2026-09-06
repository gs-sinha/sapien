# Order Timeline

## Why you must poll

The timeline is not available the instant an order is created. For a short
window after `POST /v1/orders` returns `201`, `GET /v1/orders/{orderId}/timeline`
responds `404` with code `TIMELINE_NOT_READY`. Clients (and flows) should
poll this endpoint with a short interval until it returns `200`, rather than
assume it is ready immediately after order creation.

### Fields

The `200` response is a `Timeline`:

- `orderId` - the order this timeline belongs to.
- `events` - an ordered list of `{status, at, note?}` entries, one per
  status transition the order has gone through.
- `etaMinutes` - estimated minutes remaining until delivery.

### Polling recommendation

A flow step that calls `order-service.getOrderTimeline` should use `until`
with a short `poll.interval` (for example `1s`) and a generous `poll.timeout`
(for example `30s`) so the flow does not fail spuriously on the mock's
startup delay.

## Related operations

- `order-service.createOrder` - starts the timeline with a `CREATED` event.
- `order-service.getOrderTimeline` - reads it back.
- `order-service.cancelOrder` - appends a `CANCELLED` event.
