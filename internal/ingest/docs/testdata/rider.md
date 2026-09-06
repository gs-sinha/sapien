# Rider Fields

## Profile

A `RiderProfile` wraps the core `Rider` record. Key fields include
`qcomSkill`, `upcoming_trips`, and `homeBase`.

## Field Reference

- `qcomSkill` (string): the rider's cold-chain / hazmat qualification.
- `upcoming_trips` (integer): count of confirmed future trips.
- `homeBase` (string): the rider's default dispatch zone.

## Related Endpoints

Fetch a rider by ID:

```
GET /v1/riders/{riderId}
```

This is the canonical way to fetch a `Rider`, managed by the rider-service.
