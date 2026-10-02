# Owis_Find_Deal_Engine

Product search across online shops (Amazon, eBay, AliExpress, Temu, SHEIN),
filtered by the shops that deliver to the user's country.

- `server/` — Go service `owis_find_deal_engine`
- `app/` — client apps (later)
- [`PLAN.md`](PLAN.md) — design and step-by-step plan

## Run the server (current legacy version)

```sh
cd server
go run .
```

Listens on `:3002`. See `server/main.go` for example requests.
