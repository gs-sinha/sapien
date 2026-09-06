# Rider Service

Rider Service is the source of truth for rider availability, skills, and
location.

## qcomSkill

`qcomSkill` on a `Rider` indicates whether the rider is trained and
equipped to carry quick-commerce (QCOM) orders. It does not indicate
whether the rider is currently online or available - check `online`
separately.

`allocation-service.allocate` requires `qcomSkill=true` for any order whose
`type` is `QCOM`; otherwise allocation fails with `NO_RIDER_AVAILABLE`. See
allocation-service's `allocation.md` for the full matching rule.

## upcomingTrips

`upcomingTrips` counts trips already assigned to a rider that have not yet
been completed. It deliberately **excludes reverse pickups** (a rider
returning an item to a merchant as part of an already-counted delivery) so
that dispatch does not double-count load for a single logical trip.

### Why this matters for allocation

A rider with a high `upcomingTrips` count is not automatically excluded
from allocation in this reference platform, but a production dispatcher
would weigh it alongside `online` and `qcomSkill` when ranking candidates.

## NO_RIDER_AVAILABLE

When `allocation-service.allocate` cannot find an eligible, online rider -
for a QCOM order, one with `qcomSkill=true` - it returns `409` with code
`NO_RIDER_AVAILABLE`. Bringing a rider online via
`PATCH /v1/riders/{riderId}/status` (`rider-service.updateRiderStatus`)
makes them eligible again.

## Searching riders

`POST /v1/riders/search` (`rider-service.searchRiders`) takes a point and
radius and an optional `qcomOnly` flag to restrict results to riders
eligible for QCOM allocation.
