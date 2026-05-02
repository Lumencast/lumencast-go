# Examples

Two reference servers showcasing the kit :

## basic-scoreboard

The canonical broadcast example : a two-player scoreboard with a
declared `__inputs.title` operator field. Mirrors the `-js` SDK
example so cross-language interop can be observed end-to-end.

```sh
go run ./examples/basic-scoreboard
```

Connect at `ws://localhost:4000/lsdp.v1` with the `demo-operator` or
`demo-viewer` token.

## trading-dashboard

A non-broadcast example : an orderbook + P&L view with 20 Hz leaf
updates. Shows that LSDP/1 is general-purpose for any leaf-grain
reactive surface, not just stream graphics.

```sh
go run ./examples/trading-dashboard
```

Connect at `ws://localhost:4001/lsdp.v1`.
