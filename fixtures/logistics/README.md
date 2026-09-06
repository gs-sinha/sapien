# Logistics fixture

A realistic three-service logistics platform used by every phase's tests
and by the demo, implementing the PRD §48 primary V1 success scenario:
create an order, allocate a rider, fetch that rider, verify it's online,
then (once the `qcomSkill` invariant is captured as a memory) build and run
a QCOM allocation test.

```
order-service       owns the order lifecycle and delivery timeline
allocation-service   matches an order to an eligible, online rider
rider-service        source of truth for rider availability and skills
```

Each service package lives under `<service>/api/` per PLAN §6:

```
<service>/api/
  openapi.yaml     # OpenAPI 3.1.0 contract
  service.yaml     # name, description, owners, concepts, environments.local
  docs/*.md        # narrative documentation, referenced by operation ID
allocation-service/api/flows/smoke.flow.yaml   # one service-owned flow
```

## Seed riders

`mock.NewWorld()` seeds six riders across two cities, a mix of
online/offline and `qcomSkill` true/false:

| riderId | name          | city      | online | qcomSkill | upcomingTrips | rating |
|---------|---------------|-----------|--------|-----------|---------------|--------|
| R123    | Asha Rao      | Bangalore | true   | true      | 1             | 4.8    |
| R124    | Farhan Sheikh | Bangalore | true   | false     | 0             | 4.5    |
| R125    | Divya Nair    | Bangalore | false  | true      | 2             | 4.9    |
| R126    | Karan Mehta   | Mumbai    | true   | true      | 0             | null   |
| R127    | Sana Iyer     | Mumbai    | false  | false     | 0             | 4.2    |
| R128    | Vikram Singh  | Mumbai    | true   | false     | 3             | 4.6    |

`qcomSkill` means the rider is eligible for quick-commerce (QCOM)
allocation; it does not imply `online`. Allocation picks the first eligible
online rider (QCOM orders require `qcomSkill=true`); with no online rider
seeded offline changes, `allocate` on a QCOM order picks **R123** first.

## Running the mock servers

```sh
go run ./cmd/sapien-fixtures
# order-service:      http://localhost:8081
# allocation-service: http://localhost:8082
# rider-service:      http://localhost:8083
```

Flags: `-order-port`, `-allocation-port`, `-rider-port`, `-require-auth`
(rejects requests without `Authorization: Bearer <token>`; off by
default). Stop with Ctrl-C (SIGINT) or SIGTERM for a graceful shutdown.

From Go tests in another package, prefer `mock.StartAll`:

```go
world := mock.NewWorld()
world.SetTimelineDelay(0) // skip the polling delay in tests
orderURL, allocURL, riderURL, stop := mock.StartAll(t, mock.Options{World: world})
defer stop() // unnecessary if t is non-nil; StartAll registers t.Cleanup
```

## Success scenario as curl

Assuming the servers are running on the default ports and auth is off:

```sh
# 1. Create a QCOM order
curl -s -X POST http://localhost:8081/v1/orders \
  -H 'Content-Type: application/json' \
  -d '{"customerId":"cust_123","type":"QCOM","pickup":{"lat":12.9716,"lng":77.5946},"drop":{"lat":12.9352,"lng":77.6146}}'
# => {"orderId":"ord_0001","status":"CREATED","type":"QCOM","customerId":"cust_123","createdAt":"...","timeline":[...]}

# 2. Allocate a rider (QCOM orders require qcomSkill=true)
curl -s -X POST http://localhost:8082/v1/allocations \
  -H 'Content-Type: application/json' \
  -d '{"orderId":"ord_0001"}'
# => {"allocationId":"alloc_0001","orderId":"ord_0001","riderId":"R123","status":"ALLOCATED","allocatedAt":"..."}

# 3. Fetch that rider and verify it's online and QCOM-eligible
curl -s http://localhost:8083/v1/riders/R123
# => {"riderId":"R123","name":"Asha Rao","online":true,"qcomSkill":true,"upcomingTrips":1,"city":"Bangalore","rating":4.8}

# 4. Poll the order timeline until it's ready (returns 404 TIMELINE_NOT_READY
#    for ~1.5s after creation)
curl -s http://localhost:8081/v1/orders/ord_0001/timeline
# => {"code":"TIMELINE_NOT_READY","message":"timeline for \"ord_0001\" is not ready yet"}   (immediately after creation)
sleep 2
curl -s http://localhost:8081/v1/orders/ord_0001/timeline
# => {"orderId":"ord_0001","events":[...],"etaMinutes":20}

# 5. Cancel the order; its allocation is released automatically
curl -s -X POST http://localhost:8081/v1/orders/ord_0001/cancel
# => {"orderId":"ord_0001","status":"CANCELLED",...}
curl -s http://localhost:8082/v1/allocations/alloc_0001
# => {"allocationId":"alloc_0001",...,"status":"RELEASED",...}
```

If no eligible rider is online, `allocate` returns `409` with
`{"code":"NO_RIDER_AVAILABLE", ...}`. Bring a rider back with
`PATCH /v1/riders/{riderId}/status -d '{"online":true}'`.

## Deviations / design notes

- The three mock services share one in-memory `World` in-process, so
  cross-service effects (allocate marks the order `ALLOCATED`, cancel
  releases the allocation) happen synchronously without real network calls
  between the mocks. This is a simplification appropriate for a fixture,
  not a model for how the real services would talk to each other.
- `SearchRiders` ignores `lat`/`lng`/`radiusKm` (this is not a geo engine)
  but honors `qcomOnly`.
- `ReleaseAllocation` on an already-released allocation returns `409` with
  code `ALLOCATION_ALREADY_RELEASED` (not specified by the task, added for
  API realism).
- Reaching `DELIVERED` order status has no HTTP-reachable path in this
  fixture (there is no "mark delivered" operation in the success
  scenario); the `409 ORDER_ALREADY_DELIVERED` branch is covered by a
  white-box test that sets the status directly.
- `GET /v1/allocations/stats` intentionally has no `operationId` in
  `allocation-service/api/openapi.yaml`, to exercise Sapien's ID synthesis.
- Validating `openapi.yaml` well-formedness without a YAML library (per
  the task's dependency constraint) is done by asserting the files exist,
  are non-empty, and start with an `openapi:` document, plus a hard-coded
  route table in `fixtures/logistics/mock/routes_test.go` asserting the
  mock servers implement every path+method declared in the specs.
