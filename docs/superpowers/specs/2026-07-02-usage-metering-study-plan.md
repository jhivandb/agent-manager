# Study Plan — Building Reliable Metering & Usage Pipelines

Companion to `2026-07-02-usage-metering-design.md`. Goal: be able to design
and implement systems like the usage metering pipeline from first principles —
knowing *why* each piece exists, not just copying the shape.

Organizing principle: each unit pairs theory with a hands-on lab, and maps to
a component of the actual spec, so studying and shipping reinforce each other.
Roughly 6 units; at a few evenings each, ~6 weeks part-time. Order matters —
each unit builds on the previous.

---

## Unit 1 — Delivery semantics and idempotency (the foundation)

The core question of any pipeline: *what happens to an event between "it
happened" and "the consumer has it" when things crash in the middle?*

**Concepts**
- At-most-once vs at-least-once vs "exactly-once" (and why exactly-once
  *delivery* is a myth — what actually exists is at-least-once delivery +
  idempotent processing).
- Idempotency keys: deterministic vs random, and why deterministic keys
  (`{meter}:{sourceUID}:{windowStart}`) let both sides dedup without
  coordination.
- Ack semantics: why "ack after durable commit" is the entire contract, and
  what breaks when someone acks before persisting.

**Read**
- *Designing Data-Intensive Applications* (Kleppmann) — ch. 8 ("The Trouble
  with Distributed Systems") and ch. 11 ("Stream Processing"). If you read
  only one thing on this list, read these two chapters.
- Search: "You Cannot Have Exactly-Once Delivery" (Tyler Treat) — short,
  sharp, canonical.

**Lab**
Write a toy producer/consumer pair over HTTP (Go). Producer sends numbered
events; consumer persists to SQLite and acks. Now `kill -9` each side at every
possible point and observe: duplicates without idempotency keys, loss with
ack-before-commit. Fix both. This one exercise makes Unit 1 permanent.

**Maps to spec:** event model, `eventId` design, commit-before-ack ingestion.

---

## Unit 2 — The outbox pattern and transactional messaging

The direct answer to "pushing straight to the billing API would have been a
disaster if it went down." The general problem is called *dual writes*: you
cannot atomically both do a thing and tell another system about it — unless
one durable store owns the handoff.

**Concepts**
- The transactional outbox: insert event + business state in one DB
  transaction; a separate relay publishes and marks published.
- Why the relay is at-least-once by construction (crash between publish and
  mark-published → republish → dedup downstream).
- Polling relay vs log-tailing (CDC/Debezium) — when each is appropriate.
- Buffering as the universal decoupler: outbox, broker topic, and dead-letter
  queue are all the same idea (durable log between producer and consumer)
  with different operational costs.

**Read**
- microservices.io — "Transactional Outbox" pattern page (and the "dual
  write problem" it links).
- Debezium blog — "Reliable Microservices Data Exchange With the Outbox
  Pattern" (read for the CDC variant even though we poll).

**Lab**
Extend Unit 1's consumer: instead of the producer pushing to a flaky
downstream directly, insert into an outbox table and write a relay goroutine
with exponential backoff + jitter. Simulate downstream downtime for an hour of
generated traffic; verify zero loss and bounded duplicates. Then delete the
outbox and push directly — watch your original design's failure mode happen.

**Maps to spec:** `usage_events` table, publisher worker, retention job.

---

## Unit 3 — Retries, backpressure, and failure isolation

What separates a pipeline that survives production from one that melts down
*because of* its own retries.

**Concepts**
- Exponential backoff with jitter; why retry storms without jitter
  synchronize and DDoS your own dependency.
- Timeouts and their relationship to retries (retry budget ≈ timeout ×
  attempts must fit inside caller patience).
- Backpressure: bounded queues, load shedding, and *where* you're allowed to
  drop (best-effort meters) vs where you must hold and retry (strict meters).
- Aggregation as load-shaping: why per-minute counters at the source make
  downstream volume proportional to cardinality, not traffic.

**Read**
- AWS Builders' Library — "Timeouts, retries, and backoff with jitter" and
  "Avoiding insurmountable queue backlogs". Both are excellent and free.
- Google SRE book — ch. 22 "Addressing Cascading Failures".

**Lab**
Take the Unit 2 relay and give the fake billing API a 1% error rate, then a
30% error rate, then hard downtime, under 100 concurrent producers. First
without jitter (watch thundering herd on recovery), then with. Graph attempt
counts. Add a bounded in-memory buffer to the producer side and decide,
explicitly, what happens when it fills.

**Maps to spec:** gateway flusher retry-until-ack, collector flush loop,
publisher backoff, the per-meter reliability classes.

---

## Unit 4 — Kubernetes informers and controller patterns

The collector is a small Kubernetes controller. This unit is the client-go
machinery needed to build it credibly.

**Concepts**
- Watch + list, shared informers, local caches, resync — and why "level-based"
  reconciliation (re-derive state from the world) beats "edge-based" (react to
  events you might miss) for crash recovery.
- Label selectors and field selectors for scoping the watch.
- Pod lifecycle fields that matter for metering: `status.startTime`,
  `status.phase`, container statuses, `deletionTimestamp`.
- Where allocation data lives: `spec.containers[].resources.requests`, and the
  gotchas (init containers, ephemeral containers, in-place resize).

**Read**
- *Programming Kubernetes* (Hausenblas & Schimanski) — ch. 1–3 (client-go,
  informers, work queues).
- Source-dive: `kube-state-metrics` (how it exposes
  `kube_pod_container_resource_requests`) and **OpenCost** — OpenCost is
  literally an allocation-based metering system (requests × time) and is the
  closest open-source relative of our collector. Read how it handles pod
  attribution and time slicing.

**Lab**
Build a mini-collector against a kind cluster: informer on pods with a label
selector, emit "pod X ran minute M with requests R" lines to stdout. Then
kill it for 3 minutes, restart, and make backfill-from-relist work. Then unit
test the minute-slicing math including partial minutes. This is a direct
prototype of `amp-usage-collector`.

**Maps to spec:** the collector, crash recovery, slice math.

---

## Unit 5 — Time, windows, and streaming aggregation

Metering is a stream-processing problem in miniature. Windowing is where
subtle billing bugs live.

**Concepts**
- Event time vs processing time; why windows must be assigned by *when the
  usage happened*, not when the event arrived.
- Tumbling windows, window alignment (UTC wall-clock), and late data — what
  happens to a count that arrives after its window was flushed.
- Watermarks (conceptually — we don't need them, but you should know why
  systems like Flink do).
- Clock skew across sources; why deterministic window boundaries + idempotency
  keys tolerate it.

**Read**
- *Streaming Systems* (Akidau, Chernyak, Lax) — ch. 1–3. The famous
  "Streaming 101/102" O'Reilly articles by Akidau are the free short version.

**Lab**
In the mini-collector, deliberately skew the clock and inject a late-closing
window; verify the idempotency key still lands the event in the right window.
Write the gateway-counter simulation: concurrent increments to windowed
counters, flusher closing windows — prove no count is attributed to the wrong
minute under concurrency (Go race detector on).

**Maps to spec:** UTC minute windows, counted-event flushing, `runningSeconds`.

---

## Unit 6 — Brokers, and metering systems in the wild

Round out the picture: know the tool you *deferred* well enough to know when
to reach for it, and calibrate against real metering products.

**Concepts**
- Kafka/Redpanda mental model: partitions, consumer groups, offsets,
  retention, `acks=all`. NATS JetStream as the lighter-weight alternative.
- Why a broker doesn't fix source-side loss (the gateway crash window) — the
  durability boundary starts at first durable write, wherever that is.
- How commercial metering systems shape their APIs: OpenMeter (open source —
  read its architecture docs; it's CloudEvents in, windowed aggregation,
  exactly the shape of our pipeline), Stripe metered billing / meter events,
  Lago.

**Read**
- Kafka: *The Definitive Guide* ch. 1 (or Redpanda's "Kafka fundamentals"
  series). NATS JetStream docs — "concepts" section.
- OpenMeter docs + repo (github.com/openmeterio/openmeter) — compare their
  ingestion/dedup/windowing decisions against our spec; where they differ,
  work out why.

**Lab**
Swap Unit 2's outbox for Redpanda (single binary, trivial local setup):
producers → topic → forwarder consumer → flaky billing API. Reproduce the
same kill tests. You should be able to articulate, from experience, why the
guarantees are the same and only the operational profile differs — the
conclusion the spec reached on paper.

**Maps to spec:** the "when the broker becomes the right call" triggers, the
future `kafka` sink.

---

## Cross-cutting habit: design-review drills

After Units 2 and 5, redo this exercise: take your original design (per-plane
collectors pushing directly to the billing API) and write a one-page failure
analysis of it — every component, "what happens if this is down for 4 hours."
Then do the same for the spec's design. The delta *is* the curriculum. Repeat
the drill on any new pipeline design before building it; it's the cheapest
review that exists.

## Done when

You can, without reference material: (1) explain why exactly-once delivery is
impossible but exactly-once *effect* is achievable, and what each side must
do; (2) sketch the outbox pattern and its failure windows on a whiteboard;
(3) write a correct minute-slicing informer loop with crash backfill;
(4) argue both sides of outbox-vs-broker for a given volume and topology, with
numbers.

---

## Appendix — Further reading

Optional depth beyond the unit readings, grouped by theme. Each entry has a
one-line reason; skip anything whose reason doesn't grab you.

### Foundational papers (short, old, still the sharpest framing)

- **"The Log: What every software engineer should know about real-time data's
  unifying abstraction"** — Jay Kreps (LinkedIn engineering blog). The single
  best conceptual piece on why outboxes, brokers, WALs, and replication are
  all one idea. Read after Unit 2.
- **"End-to-End Arguments in System Design"** — Saltzer, Reed & Clark. Why
  dedup and delivery guarantees ultimately belong at the endpoints (your
  idempotency keys), not in the transport — no matter how reliable the
  middle claims to be.
- **"Life Beyond Distributed Transactions"** — Pat Helland. Why systems that
  can't share a transaction coordinate through idempotent messages instead;
  the theory under the whole spec.
- **"Idempotence Is Not a Medical Condition"** — Pat Helland (ACM Queue).
  Companion piece; message retries and dedup treated rigorously but readably.
- **"The Dataflow Model"** — Akidau et al. (VLDB 2015). The paper behind
  *Streaming Systems*; read if Unit 5 hooks you. Its predecessor
  **"MillWheel"** (VLDB 2013) covers low-level exactly-once state handling.
- **"The Tail at Scale"** — Dean & Barroso (CACM). Latency tails and hedging;
  context for why timeout/retry budgets matter at fan-in points like the
  ingestion API.

### Engineering blog posts (practitioners on exactly these problems)

- **Stripe — "Designing robust and predictable APIs with idempotency"**. The
  canonical producer-side idempotency-key writeup, from the people billing
  systems copy.
- **Brandur Leach — "Transactionally Staged Job Drains in Postgres"** and
  **"Implementing Stripe-like Idempotency Keys in Postgres"** (brandur.org).
  The two posts closest to our exact design: Postgres as a durable staging
  buffer with a drain worker. Read alongside the Unit 2 lab.
- **Confluent — "Exactly-once semantics are possible: Here's how Apache
  Kafka does it"**. What "exactly-once" actually means in Kafka
  (transactions + idempotent producer) and its boundaries — inoculates you
  against marketing uses of the term.
- **Segment — "Delivering billions of messages exactly once"**. A production
  war story of at-least-once + dedup at scale, including what their dedup
  store cost them.
- **Marc Brooker's blog** (brooker.co.za) — especially the posts on retries,
  jitter, and "exactly-once" — an AWS distinguished engineer writing short,
  quantitative posts; the mathematical follow-on to the Builders' Library
  pieces in Unit 3.
- **Jepsen analyses** (jepsen.io) — pick one for a system you use (Kafka,
  NATS, Postgres). Reading how guarantees fail under partitions recalibrates
  how much you trust the words in any vendor's docs.

### Books (broader shelves, dip in as needed)

- **_Release It!_ (2nd ed.)** — Michael Nygard. Stability patterns: circuit
  breakers, bulkheads, timeouts. The production-hardening mindset for Unit 3;
  the war stories alone justify it.
- **_Enterprise Integration Patterns_** — Hohpe & Woolf. The original
  catalogue of messaging patterns (dead-letter channel, guaranteed delivery,
  message expiration). Dated examples, timeless vocabulary.
- **_Database Internals_** — Alex Petrov. Part I explains WALs, fsync, and
  what "durable" physically means — the layer under the outbox's promises.
- **_Understanding Distributed Systems_** — Roberto Vitillo. If DDIA feels
  heavy, this is the approachable single-volume alternative covering the same
  ground at a practical altitude.
- **_The Site Reliability Workbook_** (free online, like the SRE book) — the
  worked-example companion; ch. on alerting on SLOs pairs with the
  self-observability section of the spec.

### Courses (when you want structure)

- **MIT 6.824 / 6.5840 Distributed Systems** — free lectures on YouTube, labs
  in Go (you implement Raft). The single highest-value structured option for
  a Go engineer; Units 1–2 here correspond roughly to its first third.

### Source code as reading (systems shaped like ours)

- **OpenMeter** (github.com/openmeterio/openmeter) — already in Unit 6; the
  ingestion/dedup path is the part to read closely.
- **OpenCost** (github.com/opencost/opencost) — already in Unit 4; allocation
  metering (requests × time) with pod attribution.
- **kube-state-metrics** — how a production informer fleet turns cluster
  state into metrics without falling over on large clusters.
- **River** (github.com/riverqueue/river) — a modern Postgres-backed job
  queue in Go by Brandur Leach & Blake Gentry; production-grade treatment of
  the same "Postgres as durable queue" primitive as our outbox, including
  locking and batch-drain details worth stealing.
