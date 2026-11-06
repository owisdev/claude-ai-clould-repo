# Owis_Find_Deal_Engine

Product search across online shops (Amazon, eBay, AliExpress, Temu, SHEIN),
limited to the shops that deliver to the user's country (USA, KSA, Jordan).

- [`server/`](server/) — Go service `owis_find_deal_engine` (how to run, API)
- `app/` — client apps (later)
- [`PLAN.md`](PLAN.md) — design and step-by-step plan
- [`docs/architecture.pdf`](docs/architecture.pdf) — architecture, processes and
  request flow explained step by step (illustrated)
- [`docs/service_guide.pdf`](docs/service_guide.pdf) — **complete current guide**
  (October 2026): architecture, Docker, flow, cache, API clients, cost, code
  map, run / use / test step by step, what is next
- [`docs/step12_guide.pdf`](docs/step12_guide.pdf) — step 12 (asynchronous
  search, signed webhooks, usage per API client): what it does and how to
  test it step by step (Git Bash commands)
- [`docs/step13_guide.pdf`](docs/step13_guide.pdf) — step 13 (MCP endpoint
  for AI agents, OpenAPI 3.1, developer docs page): what it does and how to
  test it step by step (Git Bash commands)
- Developer docs of the running server: `/docs/` and
  `/api/v1/openapi.json` (source in
  [`server/internal/devdocs`](server/internal/devdocs))
- [`STATUS.md`](STATUS.md) — where the project stands, how to run it
  locally, how to resume
