# **Product Requirements Document**

## **Agent-Native API Development, Knowledge & Testing Workspace**

**Status:** Concept / Technical Planning Input  
 **Working name:** TBD  
 **Primary audience:** Engineering and technical-planning agents  
 **Product category:** API development, discovery, documentation, testing, operational knowledge, and agent tooling

---

# **1\. Product Summary**

Build a lightweight, local-first API development environment designed around **cross-service flows, accumulated API knowledge, and agent-native operation** rather than collections of isolated HTTP requests.

The system consists of a common **API Engine** consumed by:

* a lightweight desktop UI,  
* a CLI,  
* MCP-compatible AI agents,  
* CI/CD systems,  
* and future programmatic clients.

Canonical API contracts and documentation remain owned by individual service repositories and stored as ordinary version-controlled files.

Developers compose multiple services into local workspaces.

The product indexes these services into a unified API catalog and allows humans and agents to:

1. discover APIs across services,  
2. understand API contracts and schemas,  
3. execute individual endpoints,  
4. compose multi-service flows,  
5. create assertions and tests,  
6. execute and debug those flows,  
7. create and modify flows using natural language,  
8. capture operational knowledge while testing,  
9. make that knowledge available to future agents,  
10. promote durable learned knowledge into canonical API documentation,  
11. perform the same operations through UI, CLI, MCP, and CI.

The product should treat AI agents as first-class consumers of API knowledge and execution capabilities rather than adding an AI chat interface on top of a conventional API client.

---

# **2\. Product Thesis**

The product is not fundamentally:

A faster Postman.

It is:

**A local API intelligence and execution engine that turns service-owned API contracts, flows, memories, and execution history into a searchable and executable workspace for humans and AI agents.**

The core knowledge model consists of four primitives:

Contracts  
    │  
    │ What should exist?  
    ▼  
Flows  
    │  
    │ How do APIs work together?  
    ▼  
Memories  
    │  
    │ What have developers learned?  
    ▼  
Runs  
    │  
    │ What actually happened?  
    ▼  
Agent / Human Understanding

These four primitives should reinforce one another.

---

# **3\. Problems**

## **3.1 Existing API clients are unnecessarily heavy**

Current API development tools may consume substantial memory and CPU for relatively simple operations.

The product should feel like a lightweight developer utility.

Performance and resource consumption are product features.

---

## **3.2 API knowledge is fragmented across microservices**

Developers frequently do not know:

* which service owns an operation,  
* which endpoint performs it,  
* which version should be used,  
* what inputs it expects,  
* which API produces data required by another,  
* or what sequence of APIs implements a business process.

Traditional API clients generally assume the user already knows which request to execute.

API discovery must therefore be a first-class feature.

---

## **3.3 API documentation drifts from implementation**

Central API collections frequently diverge from the actual service implementation.

Canonical API contracts should therefore live alongside source code and be owned by the service maintaining them.

---

## **3.4 API contracts do not capture operational knowledge**

Formal API specifications can describe:

* endpoints,  
* parameters,  
* schemas,  
* authentication,  
* responses,  
* examples,  
* and field descriptions.

They are less suitable for continuously accumulating knowledge such as:

`qcomSkill=true` means the rider is eligible for quick-commerce orders.

A 409 `NO_RIDER_AVAILABLE` is expected when the candidate pool is empty.

`upcomingTrips` excludes reverse pickup trips.

In staging, timeline generation may take several seconds after order creation.

When testing QCOM allocation, verify that the allocated rider has `qcomSkill=true`.

This knowledge is often learned while developers actually test and debug systems.

Existing API tooling generally loses this information in:

* Slack messages,  
* tribal knowledge,  
* personal notes,  
* debugging sessions,  
* old tickets,  
* or developers’ memories.

The product should allow this knowledge to accumulate naturally.

---

## **3.5 Multi-service testing is tedious**

Testing a business operation commonly requires:

* calling API A,  
* extracting data,  
* passing it into API B,  
* calling API C,  
* waiting for state changes,  
* inspecting multiple responses,  
* and validating business invariants.

Flows should make these workflows first-class.

---

## **3.6 API documentation is tedious to maintain**

Developers should not need to manually recreate APIs inside a separate application.

Standards such as OpenAPI and coding agents should minimize documentation overhead.

---

## **3.7 Existing API tools are not agent-native**

AI agents should be able to:

* discover APIs,  
* inspect schemas,  
* retrieve relevant knowledge,  
* understand known gotchas,  
* execute endpoints,  
* create flows,  
* modify flows,  
* create assertions,  
* run tests,  
* inspect execution history,  
* and diagnose failures.

This should happen through structured interfaces rather than UI automation.

---

# **4\. Core Product Principles**

## **4.1 Local first**

Core functionality must work without a cloud account.

A developer should be able to install the product, point it at local repositories, and immediately use it.

---

## **4.2 Git native**

Canonical shareable artifacts should be ordinary files suitable for:

* Git,  
* pull requests,  
* diffs,  
* code review,  
* branch switching,  
* coding-agent modification.

---

## **4.3 Service ownership**

Services own their canonical API contracts.

Workspaces compose services.

---

## **4.4 Standards first**

OpenAPI should be the initial canonical format for HTTP API contracts.

Do not invent a proprietary replacement for OpenAPI.

Future protocol support may include:

* gRPC/protobuf,  
* GraphQL,  
* WebSockets,  
* asynchronous/event interfaces.

---

## **4.5 OpenAPI is the contract layer, not the entire knowledge layer**

OpenAPI should primarily answer:

What operation exists?

What can I send?

What can I receive?

What does this operation or field formally mean?

The product should not force all business semantics, testing experience, operational behavior, and learned knowledge into OpenAPI.

---

## **4.6 Flow first**

The primary UI abstraction is a flow.

An individual endpoint execution can be treated as a one-step flow.

A test is a flow containing assertions.

A regression test is a parameterized flow.

CI executes flows.

---

## **4.7 Knowledge accumulates through usage**

Testing and debugging should make the workspace smarter over time.

Developers should be able to capture useful observations with minimal friction.

Agents should benefit from those observations later.

---

## **4.8 Agent native**

Anything important that humans can perform through the UI should have a structured programmatic equivalent.

At minimum:

* engine API,  
* CLI,  
* MCP.

---

## **4.9 Deterministic execution**

LLMs assist with authoring and reasoning.

They should not define runtime semantics.

Preferred architecture:

Natural language  
      ↓  
LLM  
      ↓  
Declarative flow  
      ↓  
Validation  
      ↓  
Deterministic execution engine  
---

## **4.10 Lightweight**

The core local system should remain small and responsive.

Avoid architectural dependencies that require heavy infrastructure for basic usage.

---

# **5\. Core Domain Model**

The system should explicitly model four major sources of API intelligence.

## **5.1 Contracts**

Formal machine-readable API definitions.

Initially:

OpenAPI 3.x

Contracts contain:

* endpoints,  
* operations,  
* request schemas,  
* response schemas,  
* parameters,  
* authentication,  
* examples,  
* formal descriptions.

---

## **5.2 Flows**

Executable compositions of APIs representing business behavior.

Examples:

Create Order  
    ↓  
Allocate Rider  
    ↓  
Fetch Rider

or:

Create Order  
    ↓  
Cancel Order  
    ↓  
Verify Allocation Released

Flows may contain assertions and therefore act as tests.

---

## **5.3 Memories**

Human or agent-curated operational knowledge discovered while using APIs.

Examples:

qcomSkill indicates eligibility for QCOM work.  
409 NO\_RIDER\_AVAILABLE is expected when no candidates exist.  
upcomingTrips does not include reverse pickups.

Memories may be scoped to:

* workspace,  
* service,  
* endpoint,  
* request field,  
* response field,  
* schema,  
* error,  
* environment,  
* flow,  
* flow step,  
* concept.

---

## **5.4 Runs**

Historical execution evidence.

Runs record what actually occurred when endpoints or flows were executed.

They may contain:

* requests,  
* responses,  
* timings,  
* assertion results,  
* failures,  
* environment,  
* flow snapshot.

Runs should provide evidence from which humans and agents can derive new memories.

---

# **6\. Conceptual Architecture**

       Service Repositories  
               │  
               ▼  
       OpenAPI \+ Documentation  
               │  
               ▼  
┌─────────────────────────────────────┐  
│              API Engine             │  
│                                     │  
│  Service Registry                   │  
│  Contract Parser                    │  
│  API Catalog                        │  
│  Search / Retrieval                 │  
│  Memory Store                       │  
│  Flow Engine                        │  
│  HTTP Runtime                       │  
│  Assertions                         │  
│  Environments / Secrets             │  
│  Run History                        │  
│  Agent Context Builder              │  
└──────────────────┬──────────────────┘  
                   │  
       ┌───────────┼──────────┬──────────┐  
       ▼           ▼          ▼          ▼  
      UI          CLI        MCP         CI

The implementation plan should determine whether the engine operates as:

* standalone local daemon,  
* embedded library,  
* or hybrid architecture.

---

# **7\. Service API Package**

A service should be capable of publishing an API package from its repository.

Example:

order-service/  
├── src/  
└── api/  
    ├── openapi.yaml  
    ├── service.yaml  
    ├── docs/  
    ├── examples/  
    └── flows/

Only the OpenAPI contract may be mandatory initially.

---

# **8\. OpenAPI Responsibilities**

OpenAPI should be used for formal API contract information.

Example:

qcomSkill:  
  type: boolean  
  description: \>  
    Indicates whether this rider is eligible  
    for quick-commerce work.

    This does not indicate whether the rider  
    is currently online or available.

If knowledge is stable, canonical, and directly describes an API field or operation, it should generally be promoted into OpenAPI documentation.

---

# **9\. Product Metadata**

Additional service-level information may live in product-specific metadata.

Example:

name: allocation-service

description: \>  
  Finds eligible riders and creates allocations.

owners:  
  \- allocation-platform

concepts:  
  \- rider allocation  
  \- dispatch  
  \- matching

environments:  
  local:  
    base\_url: http://localhost:8080

The implementation plan should define the minimal metadata schema.

---

# **10\. Workspace**

A workspace is a developer’s composition of services.

Example:

Logistics

order-service  
allocation-service  
rider-service  
pricing-service  
payment-service

Different developers may compose different workspaces from the same canonical services.

---

# **11\. Adding Services**

Support at least:

### **Local repository**

tool service add \~/code/order-service

Local filesystem changes should automatically update the API catalog.

The product must not modify Git state or automatically pull the developer’s working repository.

### **Remote Git repository**

tool service add git@github.com:company/order-service.git

The product may maintain a managed local clone/cache and periodically fetch updates.

---

# **12\. API Catalog**

Normalize all workspace APIs into a unified internal catalog.

Each endpoint should expose:

* service,  
* protocol,  
* operation ID,  
* method,  
* path,  
* summary,  
* description,  
* parameters,  
* request schema,  
* response schemas,  
* authentication requirements,  
* examples,  
* concepts/tags,  
* source location.

The normalized representation must not be tightly coupled to OpenAPI so future protocols can be supported.

---

# **13\. API Discovery**

Users and agents should be able to search:

POST /v1/orders

as well as:

create order

or:

find riders around a pickup

Search should operate across all workspace services.

The implementation plan should evaluate:

* structured lookup,  
* lexical/full-text search,  
* semantic retrieval,  
* optional LLM reranking.

Basic search must not require an LLM.

---

# **14\. Endpoint Execution**

Users should be able to execute individual endpoints.

Support:

* path parameters,  
* query parameters,  
* headers,  
* request bodies,  
* authentication,  
* environment variables,  
* secrets,  
* timeout,  
* response headers,  
* response body,  
* latency,  
* HTTP status,  
* error inspection.

---

# **15\. Flow Model**

A flow is the universal executable artifact.

Conceptual representation:

version: 1

name: Order allocation

steps:

  \- id: create  
    call: order.create  
    input:  
      customerId: ${env.TEST\_CUSTOMER}

  \- id: allocate  
    call: allocation.allocate  
    input:  
      orderId: ${steps.create.body.orderId}

  \- id: rider  
    call: rider.get  
    input:  
      riderId: ${steps.allocate.body.riderId}

    assert:  
      \- response.status \== 200  
      \- response.body.online \== true

The implementation plan must define the final DSL.

---

# **16\. Flow Requirements**

V1 flows should support:

* sequential endpoint execution,  
* static values,  
* environment values,  
* secrets,  
* flow inputs,  
* previous-step outputs,  
* extraction,  
* assertions,  
* latency assertions,  
* schema assertions,  
* step execution state,  
* cancellation,  
* failure diagnostics.

The representation should permit future support for:

* conditions,  
* branches,  
* parallel execution,  
* retries,  
* loops,  
* datasets,  
* setup/teardown,  
* reusable subflows.

---

# **17\. Tests**

Tests should not be a separate core abstraction.

A test is:

A flow with assertions.

A regression suite may consist of multiple flows and/or parameterized executions.

The same flow should run through:

* UI,  
* CLI,  
* MCP,  
* CI.

---

# **18\. Memory Model**

Memory is a first-class domain object.

A memory should minimally contain:

ID  
text  
scope  
subject/reference  
source  
created timestamp  
updated timestamp

Potential richer representation:

id: mem\_123

subject:  
  service: rider-service  
  endpoint: getRider  
  field: response.qcomSkill

type: semantic

text: \>  
  qcomSkill indicates whether the rider  
  is eligible for quick-commerce orders.

implications:  
  \- QCOM allocation should only select  
    riders where qcomSkill=true.

source:  
  type: user

scope: workspace

The technical plan should determine how much structure should exist in V1.

Do not require users to fill structured forms merely to save a note.

Natural-language capture should remain the default experience.

---

# **19\. Memory Types**

The system should consider supporting classifications such as:

### **Semantic**

Meaning of fields or concepts.

`qcomSkill` means eligibility for quick-commerce work.

### **Behavioral**

Observed API behavior.

Allocation returns 409 when no eligible riders exist.

### **Testing**

Useful testing knowledge.

Wait for timeline generation before asserting ETA.

### **Invariant**

Expected cross-service business behavior.

QCOM orders must be allocated only to QCOM-enabled riders.

### **Environment-specific**

Knowledge applying to a specific environment.

Rider R123 is the standard staging test rider.

### **Gotcha**

Unexpected behavior or legacy semantics.

`upcomingTrips` excludes reverse pickups.

These classifications should aid retrieval but should not create substantial capture friction.

---

# **20\. Memory Scope**

Memories should support at least:

### **Personal**

Visible only to the local developer.

### **Workspace**

Shared conceptually across the local workspace.

### **Service**

Associated with a particular service.

### **Flow**

Associated with a particular flow.

Future team/cloud versions may introduce organization-level sharing.

Scope and subject are separate concepts.

For example, a workspace-visible memory may specifically reference one endpoint field.

---

# **21\. Capturing Memories During Testing**

Memory capture should be available directly from execution results.

Example response:

{  
  "riderId": "R123",  
  "qcomSkill": true,  
  "upcomingTrips": 2  
}

The user should be able to select:

qcomSkill

and choose:

Remember

Then enter:

This determines whether the rider is eligible for QCOM allocation.

The product should automatically attach available context:

* service,  
* endpoint,  
* response field,  
* flow,  
* run,  
* environment.

Users should not need to manually recreate this context.

---

# **22\. Memory From Runs**

A memory may originate from an execution.

Example:

Run:  
QCOM allocation

Observed:  
allocated rider qcomSkill \= true

User:

This is an important invariant. Remember it.

The system should be capable of creating:

QCOM allocations should produce riders  
where qcomSkill=true.

and attaching the relevant endpoint/flow context.

The original run may be retained as provenance where appropriate.

---

# **23\. Agent Memory Retrieval**

Agents should not receive all memories globally.

Memory retrieval should be contextual.

Example:

User:  
"Create a QCOM allocation test."

        ↓

Search APIs  
        ↓

order.create  
allocation.allocate  
rider.get

        ↓

Retrieve knowledge relevant to:  
allocation  
rider  
QCOM  
qcomSkill

        ↓

Construct flow

This should prevent context explosion in large organizations.

---

# **24\. Agent Context Builder**

Introduce an explicit engine component responsible for assembling agent context.

Given:

intent  
selected APIs  
selected flow  
environment

it should retrieve relevant:

* endpoint contracts,  
* schemas,  
* documentation,  
* memories,  
* related flows,  
* optionally relevant recent run evidence.

This component should be independent of any specific LLM vendor.

---

# **25\. Memory Retrieval Strategy**

The technical implementation plan should evaluate hybrid retrieval using:

1. direct entity association,  
2. service association,  
3. endpoint association,  
4. field/schema association,  
5. lexical search,  
6. semantic similarity,  
7. recency,  
8. memory confidence/importance.

Explicit structural associations should generally rank above semantic similarity.

Example:

A memory explicitly attached to:

rider.get.response.qcomSkill

should usually outrank a vaguely similar memory attached to another service.

---

# **26\. Memory Provenance**

Where practical, memories should retain their origin.

Potential sources:

user  
agent  
run  
import  
documentation

Agent-generated memories should be distinguishable from user-authored memories.

Future versions may introduce:

* confidence,  
* verification,  
* endorsement,  
* expiration,  
* conflict detection.

---

# **27\. Memory → Documentation Promotion**

Users should be able to promote durable memories into canonical documentation.

Example memory:

upcomingTrips excludes reverse pickups.

Action:

Promote to documentation

The agent may propose:

upcomingTrips:  
   type: integer  
\+  description: \>  
\+    Number of upcoming forward-delivery trips.  
\+    Reverse pickup trips are not included.

The change should occur through the normal service repository workflow.

The product should not silently modify canonical documentation.

---

# **28\. Knowledge Lifecycle**

The intended lifecycle is:

Developer tests API  
       ↓  
Discovers useful behavior  
       ↓  
Saves memory  
       ↓  
Future agents use memory  
       ↓  
Knowledge proves durable  
       ↓  
Promote to canonical docs  
       ↓  
Git review  
       ↓  
Service contract becomes richer

The product should make this lifecycle inexpensive.

---

# **29\. Memory → Test Promotion**

Memories describing invariants should be convertible into assertions or flows.

Example memory:

QCOM allocations must return QCOM-enabled riders.

Action:

Turn into test

Potential generated flow:

Create QCOM Order  
       ↓  
Allocate Rider  
       ↓  
Fetch Rider  
       ↓  
assert qcomSkill \== true

The user should review generated changes before they become canonical tests.

---

# **30\. Natural-Language Flow Authoring**

Users should be able to say:

Create an order, allocate a rider, fetch the rider and verify that the rider is online.

The system should:

1. interpret intent,  
2. search actual registered APIs,  
3. inspect candidate contracts,  
4. retrieve relevant memories,  
5. inspect relevant existing flows,  
6. determine data dependencies,  
7. generate declarative flow,  
8. validate endpoint references,  
9. validate field references,  
10. present proposed flow,  
11. execute only after appropriate user action.

The agent should not fabricate unavailable APIs.

---

# **31\. Memory-Aware Flow Authoring**

Memory should materially influence flow creation.

Example:

User:

Test QCOM allocation.

Retrieved memory:

QCOM allocation requires qcomSkill=true riders.

The agent should be capable of proposing:

Create QCOM Order  
      ↓  
Allocate Rider  
      ↓  
Fetch Allocated Rider  
      ↓  
assert qcomSkill \== true

This is a core success scenario for the memory system.

---

# **32\. Natural-Language Flow Editing**

Users should be able to modify flows with instructions such as:

Also verify allocation completes within two seconds.

Use the order ID returned from the first call.

Add the QCOM rider invariant.

Replace the allocation v1 endpoint with v2.

The resulting declarative flow should remain inspectable.

---

# **33\. UI Product Model**

Primary navigation:

Workspace  
├── Flows  
├── Services  
├── Environments  
└── Runs

Memory should generally appear **contextually** rather than requiring a major separate navigation surface in V1.

Examples:

Endpoint  
  ├── Contract  
  ├── Examples  
  └── Knowledge

Flow  
  ├── Steps  
  ├── Runs  
  └── Knowledge

A global knowledge search may be added if needed.

---

# **34\. Flows UI**

Flows are the primary/home experience.

Users should be able to:

* create flows,  
* create flows with natural language,  
* edit flows,  
* edit flows with natural language,  
* execute complete flows,  
* execute individual steps,  
* inspect requests/responses,  
* add assertions,  
* save observations as memories,  
* view relevant knowledge,  
* diagnose failures.

Avoid prematurely building an unconstrained graph canvas.

A structured sequential flow editor is acceptable for V1.

---

# **35\. Services UI**

Users should be able to:

* add services,  
* browse services,  
* search endpoints,  
* inspect contracts,  
* inspect schemas,  
* inspect relevant memories,  
* execute endpoints,  
* save observations,  
* identify flows using endpoints.

---

# **36\. Runs UI**

Run history should include:

* flow snapshot,  
* environment,  
* timestamp,  
* duration,  
* steps,  
* requests,  
* responses,  
* assertions,  
* errors.

From any relevant piece of a run, users should be able to invoke:

Remember this

Context should automatically be captured.

---

# **37\. CLI**

The CLI is first-class.

Conceptual commands:

tool service add ./order-service

tool search "allocate rider"

tool describe allocation.allocate

tool call allocation.allocate

tool flow run order-allocation

tool memory add \\  
  \--endpoint rider.get \\  
  "qcomSkill indicates QCOM eligibility"

tool memory search "QCOM"

tool memory list \--endpoint rider.get

Exact command syntax is a technical-design decision.

Machine-readable JSON output should be supported.

---

# **38\. MCP**

Initial conceptual MCP operations:

list\_services()  
search\_apis(query)  
get\_api(api\_id)

execute\_api(api\_id, input)

list\_flows()  
get\_flow(flow\_id)  
create\_flow(flow)  
update\_flow(flow)  
validate\_flow(flow)  
run\_flow(flow\_id)

get\_run(run\_id)

search\_memories(query, context)  
get\_relevant\_memories(subjects)  
create\_memory(...)

Potentially sensitive write operations should be permissioned appropriately.

MCP should optimize for progressive retrieval:

search  
   ↓  
inspect  
   ↓  
retrieve knowledge  
   ↓  
execute

Avoid sending the entire API catalog or memory store into agent context.

---

# **39\. LLM Provider Independence**

The architecture must not depend on a single model provider.

Possible providers may include:

* OpenAI,  
* Anthropic,  
* Gemini,  
* local/OpenAI-compatible models,  
* external coding agents using MCP.

The engine and flow runtime must work without an LLM.

---

# **40\. Source Synchronization**

For local repositories:

filesystem change  
       ↓  
incremental parse  
       ↓  
validation  
       ↓  
catalog/index update  
       ↓  
client notification

For managed Git sources:

fetch  
  ↓  
detect changed artifacts  
  ↓  
incremental re-index

Do not automatically manipulate developers’ working branches.

---

# **41\. Branch Awareness**

The catalog should reflect the currently checked-out local branch.

Future capability:

tool api diff main

Example:

ADDED  
POST /v2/riders/search

REMOVED  
GET /v1/riders/nearby

CHANGED  
POST /allocation  
  maxDistance:  
    number → integer

Not required for V1 unless inexpensive.

---

# **42\. Persistence**

Canonical shareable artifacts should remain file-backed where practical.

An embedded database may store:

* normalized catalog,  
* search indexes,  
* personal/workspace memories,  
* run history,  
* application state,  
* caches.

The catalog/index should be rebuildable from canonical source artifacts.

The implementation plan should explicitly determine which memory scopes are file-backed versus database-backed.

---

# **43\. Memory Conflict and Staleness**

V1 does not need sophisticated knowledge management, but the architecture should anticipate that memories can become wrong.

Potential future states:

active  
superseded  
deprecated  
disputed

Potential future mechanisms:

* user verification,  
* last verified timestamp,  
* linked contract version,  
* linked commit,  
* conflict detection,  
* agent warnings.

Agents must not treat arbitrary memories as equivalent to canonical contracts.

Knowledge hierarchy should roughly favor:

Current formal contract  
        ↓  
Verified canonical documentation  
        ↓  
Explicit shared memories  
        ↓  
Personal memories  
        ↓  
Agent-inferred observations

Context and provenance may alter this ordering.

---

# **44\. Security**

The technical plan must explicitly cover:

### **Secrets**

Use secure credential storage.

Do not casually persist API credentials in workspace files.

### **MCP permissions**

Agents may need separate permissions for:

* reading contracts,  
* reading memories,  
* writing memories,  
* executing read APIs,  
* executing mutation APIs,  
* accessing environments,  
* modifying flows.

### **Production safeguards**

Production must be clearly distinguished from test environments.

### **Run redaction**

Sensitive headers and response fields may require redaction before persistence.

### **Memory leakage**

A saved memory may itself contain sensitive information.

Memory storage, retrieval, sharing, and agent exposure therefore require security boundaries.

---

# **45\. Performance**

The product should target:

* near-instant normal startup,  
* low idle CPU,  
* modest idle memory,  
* responsive operation with thousands of endpoints,  
* incremental indexing,  
* fast lexical search,  
* fast structurally scoped memory retrieval.

Semantic retrieval should not make normal interactions feel slow.

The technical plan should define measurable benchmarks.

---

# **46\. V1 Scope**

V1 should include:

1. Local workspace.  
2. Local service repositories.  
3. Git-backed services.  
4. OpenAPI ingestion.  
5. Automatic source refresh.  
6. Unified API catalog.  
7. API search.  
8. Individual HTTP execution.  
9. Environments.  
10. Secure secret handling.  
11. Declarative flows.  
12. Sequential multi-endpoint execution.  
13. Data passing.  
14. Assertions.  
15. Run history.  
16. Flow-first desktop UI.  
17. CLI.  
18. MCP server.  
19. Natural-language flow generation.  
20. Natural-language flow editing.  
21. Manual memory creation.  
22. Contextual memory capture from endpoint/flow runs.  
23. Memory association with services/endpoints/fields/flows.  
24. Contextual memory retrieval for agents.  
25. Memory-aware flow generation.  
26. Basic memory search.  
27. Provenance for memories.

Memory → documentation and memory → test promotion should be included if feasible but may follow the initial V1 vertical slice.

---

# **47\. Explicit V1 Non-Goals**

Do not initially build:

* hosted collaboration platform,  
* enterprise knowledge graph,  
* sophisticated automatic memory generation,  
* automatic trust/confidence scoring,  
* automatic contract modification,  
* API monitoring SaaS,  
* mock servers,  
* load testing,  
* arbitrary JavaScript runtime,  
* enterprise RBAC,  
* full API protocol coverage,  
* traffic capture,  
* OpenTelemetry-derived graphs,  
* autonomous production debugging.

---

# **48\. Primary V1 Success Scenario**

A developer adds:

order-service  
allocation-service  
rider-service

Each provides OpenAPI definitions.

The developer searches:

allocate rider

and discovers the appropriate endpoint.

The developer can execute it independently.

The developer then asks:

Create an order, allocate a rider, fetch that rider and verify the rider is online.

The product creates:

Create Order  
      ↓  
Allocate Rider  
      ↓  
Fetch Rider  
      ↓  
Assert Online

The developer executes it.

During debugging, the developer discovers:

qcomSkill=true means the rider  
is eligible for QCOM allocation.

From the response viewer they select the field and choose:

Remember

They save:

QCOM allocations should only select riders with qcomSkill=true.

Later they ask:

Create a QCOM allocation test.

The agent:

1. discovers the relevant APIs,  
2. retrieves the saved memory,  
3. recognizes the cross-service invariant,  
4. constructs:

Create QCOM Order  
       ↓  
Allocate Rider  
       ↓  
Fetch Rider  
       ↓  
Assert qcomSkill \== true

The developer reviews and executes the flow.

The same flow can subsequently run through:

UI  
CLI  
MCP  
CI

This demonstrates the primary product thesis:

**Using and testing APIs causes the system to accumulate knowledge that makes both developers and agents progressively better at understanding and testing the system.**

---

# **49\. Technical Planning Questions**

The implementation-planning agent should explicitly design:

## **Engine**

* daemon vs embedded architecture,  
* concurrency model,  
* API boundaries,  
* event model.

## **Normalized API representation**

* OpenAPI parsing,  
* protocol-independent domain model,  
* schema representation.

## **Flow DSL**

* syntax,  
* versioning,  
* variables,  
* references,  
* assertions,  
* future DAG support.

## **Memory schema**

Define:

* identity,  
* text,  
* type,  
* scope,  
* subject,  
* provenance,  
* timestamps,  
* optional structured implications.

Avoid over-structuring V1.

## **Memory subject model**

Determine how memories attach robustly to:

* service,  
* endpoint,  
* request field,  
* response field,  
* schema,  
* flow,  
* run,  
* environment.

References should survive reasonable OpenAPI changes where possible.

## **Memory storage**

Determine:

* personal storage,  
* workspace storage,  
* service-shared storage,  
* Git-backed memories,  
* embedded database usage.

## **Memory retrieval**

Design ranking using:

* direct associations,  
* endpoint/service relationships,  
* lexical relevance,  
* semantic relevance,  
* scope,  
* provenance,  
* recency.

## **Agent context builder**

Specify how the engine progressively gathers:

intent  
→ candidate APIs  
→ contracts  
→ memories  
→ flows  
→ run evidence

without context explosion.

## **LLM integration**

Define tools exposed to the flow-authoring model.

Prefer:

search\_api  
inspect\_api  
search\_memory  
inspect\_flow  
validate\_flow

over bulk context injection.

## **Execution**

Define:

* request lifecycle,  
* variables,  
* authentication,  
* cancellation,  
* error propagation,  
* assertions,  
* run persistence.

## **Search**

Evaluate:

* SQLite FTS,  
* structured filters,  
* local embeddings,  
* hybrid retrieval.

## **Git**

Define managed clones, credentials, branches, and source updates.

## **Desktop**

Evaluate Tauri or equivalent lightweight architecture.

## **IPC**

Define UI/CLI/MCP interaction with the engine.

## **MCP**

Define:

* tools,  
* resources,  
* permissions,  
* context efficiency.

## **Secrets**

Define OS-native secure storage and agent-safe substitution.

## **Security**

Specifically threat-model:

* agent execution,  
* production environments,  
* secrets,  
* sensitive run data,  
* sensitive memories.

## **Performance**

Benchmark:

* cold start,  
* idle memory,  
* 1,000 endpoints,  
* 10,000 endpoints,  
* API search,  
* memory retrieval,  
* incremental indexing,  
* flow overhead.

---

# **50\. Required Technical Implementation Plan**

Using this PRD, produce a detailed implementation plan containing:

1. System architecture.  
2. Component diagram.  
3. Technology choices.  
4. Repository/module structure.  
5. API engine architecture.  
6. Normalized endpoint model.  
7. Service package specification.  
8. Workspace specification.  
9. Flow DSL specification.  
10. Flow execution state machine.  
11. Memory domain model.  
12. Memory subject/reference model.  
13. Memory persistence strategy.  
14. Memory retrieval/ranking architecture.  
15. Agent context-building algorithm.  
16. Persistence schema.  
17. Search architecture.  
18. Filesystem synchronization.  
19. Git synchronization.  
20. HTTP runtime.  
21. Environment and secrets architecture.  
22. CLI specification.  
23. MCP specification.  
24. Natural-language flow generation.  
25. Memory-aware flow generation.  
26. Desktop architecture.  
27. Engine/UI IPC.  
28. Security model.  
29. Error model.  
30. Observability.  
31. Testing strategy.  
32. Performance benchmark plan.  
33. Packaging/distribution.  
34. Phased implementation roadmap.  
35. Engineering risks and mitigations.

For every significant architectural choice, provide:

* alternatives considered,  
* recommendation,  
* rationale,  
* tradeoffs,  
* future implications.

Pay particular attention to the boundary between:

OpenAPI contract  
vs  
service metadata  
vs  
memory  
vs  
flow  
vs  
run evidence

Do not allow these concepts to collapse into a single proprietary documentation format.

Optimize the architecture for the V1 success scenario while maintaining clear extension points for future API protocols, team knowledge sharing, API graphs, CI execution, hosted collaboration, and enterprise agent governance.

