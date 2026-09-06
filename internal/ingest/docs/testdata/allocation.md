# Allocation Rules

This document explains rider allocation and dispatch for the allocation-service.

## Overview

The `allocation-service` finds eligible riders for an order and creates an
`Allocation`. Rider allocation depends on a rider's `qcomSkill`.

## QCOM Skill Matching

A `Rider` is only eligible when its `qcomSkill` matches the order's required
skill. Riders (plural) are never scored individually here.

## Endpoints

Create an allocation:

```
POST /v1/allocations
```

Fetch a rider's allocation:

```
GET /v1/riders/{riderId}/allocation
```

Looking up a concrete rider, e.g. GET /v1/riders/R123, returns the same shape
but is not a tracked operation on its own.

See also `allocation-service.allocate` for the underlying operation, or ask
the rider-service team.

Example request (a rider record is referenced only in the code comment
below and must not be picked up as a schema reference):

```
# Rider must already exist
POST /v1/allocations
```
