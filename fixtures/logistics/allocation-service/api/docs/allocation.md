# Allocation Service

Allocation Service matches an existing order (see order-service) to an
eligible, online rider (see rider-service).

## QCOM allocation rules

`POST /v1/allocations` (operation `allocation-service.allocate`) matches an
order to a rider. The matching rule depends on the order's `type`:

- `STANDARD` orders may be matched to any online rider.
- `QCOM` orders may only be matched to a rider with `qcomSkill=true` who is
  also online. See rider-service's `riders.md` for what `qcomSkill` means.

### No eligible rider

If no rider satisfies the rule above, `allocation-service.allocate` returns
`409` with code `NO_RIDER_AVAILABLE`. Callers should treat this as a
retryable condition, not a hard failure - bringing a rider online, or a
current allocation being released, can make an eligible rider available
again.

## Releasing an allocation

`POST /v1/allocations/{allocationId}/release`
(`allocation-service.releaseAllocation`) frees the rider so they become
eligible for the next allocation. Cancelling an order (see order-service's
`orders.md`) releases its allocation automatically as part of
`order-service.cancelOrder`.

## Inspecting allocations

`GET /v1/allocations/{allocationId}` (`allocation-service.getAllocation`)
and `GET /v1/allocations` (`allocation-service.listAllocations`, filterable
by `orderId` and `riderId`) are read-only lookups.

`GET /v1/allocations/stats` returns aggregate counts and deliberately has no
`operationId` in the OpenAPI document, to exercise ID synthesis.

## Deprecated endpoint

`POST /v1/allocate` (`allocation-service.allocateV1`) is deprecated; new
integrations should use `POST /v1/allocations` instead.
