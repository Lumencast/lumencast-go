# Scenario format

Each scenario describes a sequence of LSDP message exchanges and expected behaviours, in YAML. A conformance runner replays the steps and verifies the implementation under test conforms.

## File location

```
conformance/v1/scenarios/<scenario-id>.yaml
```

The `<scenario-id>` matches the file name (without extension). Indexed in [`../manifest.json`](../manifest.json).

## Top-level shape

```yaml
name: subscribe-snapshot-delta
description: Basic happy path — subscribe, receive snapshot, receive delta.
tag: required          # required | recommended | extended
target: any            # any | server | runtime
spec_refs:
  - LSDP-1#section-6
  - LSDP-1#section-3.1

steps:
  - <step>
  - <step>
  ...
```

| Field | Required | Description |
|---|---|---|
| `name` | yes | Scenario identifier. MUST match the filename. |
| `description` | yes | One-sentence English summary of what the scenario verifies. |
| `tag` | yes | `required` (must pass for conformance), `recommended` (quality signal), `extended` (corner cases / perf). |
| `target` | yes | `any` (drives both server and runtime), `server` (drives a server, runner acts as client), `runtime` (drives a runtime, runner acts as server). |
| `spec_refs` | no | List of section anchors in the spec docs that this scenario covers. |
| `steps` | yes | Ordered sequence of step objects. |

## Step kinds

Each step is an object with a `kind` field. The runner executes steps in order.

### `client-sends`

The client (operator-side) sends a frame. The runner emits this on behalf of the client.

```yaml
- kind: client-sends
  frame:
    v: 1
    type: subscribe
    token: $TOKEN_OPERATOR
```

Tokens are `$VARIABLE` placeholders resolved by the runner from the `tokens:` section (if any) at the scenario top level. The runner provides default tokens for standard roles.

### `server-sends`

The server sends a frame. When `target: runtime`, the runner emits this; when `target: server`, the runner expects the server-under-test to emit a matching frame.

```yaml
- kind: server-sends
  frame:
    v: 1
    type: snapshot
    seq: 1
    scene_id: test-scene
    scene_version: sha256:0000000000000000000000000000000000000000000000000000000000000000
    state:
      title: Hello
      count: 0
```

When the runner is in **expect** mode (target: server), `frame` matching follows these rules:

- All literal fields must match exactly.
- `$ANY` placeholder matches any value of any type (used for non-deterministic fields like timestamps).
- `$ANY_HASH` matches any sha256 hash format.
- Field order doesn't matter (JSON object semantics).
- Unknown fields in the actual frame are tolerated (forward compat).
- Missing required fields fail.

### `expect-runtime-state`

Verify the runtime's reactive store matches.

```yaml
- kind: expect-runtime-state
  state:
    title: Hello
    count: 0
```

Only valid when the runner has access to the runtime's state — typically `target: runtime` or `target: any`.

The state shape MUST be a flat dictionary of `LeafPath → JSONValue`. Sub-objects in the expected state are flattened to dotted paths.

### `expect-server-state`

Verify the server's authoritative state matches.

```yaml
- kind: expect-server-state
  state:
    "__inputs.title": World
```

Same shape as `expect-runtime-state`, but for the server side.

### `expect-client-action`

Verify the runtime took an expected action that doesn't directly emit a frame (e.g., closes the WebSocket, attempts a reconnect, sets a status).

```yaml
- kind: expect-client-action
  action: close-with-reason
  reason: VERSION_GAP
```

Defined actions:

| `action` | Meaning | Additional fields |
|---|---|---|
| `close-with-reason` | The runtime closes the WS with a reason string | `reason` |
| `reconnect` | The runtime opens a fresh WS to the same URL | (none) |
| `fetch-bundle` | The runtime issues a HTTP GET for `<scene_version>` bundle | `scene_version` |
| `status-change` | The runtime emits an `onStatus` callback | `status` (one of `disconnected`/`connecting`/`live`) |
| `onError` | The runtime emits an `onError` callback | `code` (matches an `ErrorCode`) |

### `expect-no-frame-for`

Verify no server frame arrives for a duration. Used to test silence (e.g., heartbeat behaviour).

```yaml
- kind: expect-no-frame-for
  duration_ms: 1000
```

### `wait`

Pause the runner for a duration. Used to test time-dependent behaviour (heartbeats, reconnect backoff).

```yaml
- kind: wait
  duration_ms: 500
```

### `set-clock`

Set the test clock to a specific point. Used for time-sensitive scenarios. Implementations that don't support a virtual clock skip steps using this kind (the scenario is then `recommended`, not `required`).

```yaml
- kind: set-clock
  to_ms: 5000
```

## Tokens & roles

Scenarios commonly need tokens with specific roles. The runner provides standard tokens by default :

| Placeholder | Role | Notes |
|---|---|---|
| `$TOKEN_VIEWER` | viewer | Cannot send `input` |
| `$TOKEN_OPERATOR` | operator | Can write to `__inputs.*` |
| `$TOKEN_SERVICE` | service | Can write to `__inputs.*` constrained by `paths` claim |
| `$TOKEN_TEST` | test | Can write to `__test.*` only |
| `$TOKEN_INVALID` | (none) | Always rejected with `AUTH_DENIED` |

Custom tokens can be declared at the top of the scenario:

```yaml
tokens:
  - placeholder: $TOKEN_SERVICE_NARROW
    role: service
    paths: ["quasar.twitch.*"]
```

## Bundle references

Scenarios that exercise scene loading reference test bundles by path :

```yaml
- kind: server-sends
  frame:
    type: snapshot
    scene_id: test-scene
    scene_version: $BUNDLE.minimal-scoreboard.hash
    state:
      "score.home": 0

bundles:
  - id: minimal-scoreboard
    path: ../bundles/minimal-scoreboard.lsml.json
```

`$BUNDLE.<id>.hash` resolves to the sha256 of the named bundle. The runner pre-computes these.

## Running a scenario

```sh
lumencast conformance --scenario subscribe-snapshot-delta --target runtime --runtime-cmd "node ./build/runtime.js"
lumencast conformance --tag required --target server --server-url ws://localhost:8080
```

The runner:

1. Loads the scenario YAML
2. Resolves placeholders ($TOKEN_*, $BUNDLE.*)
3. For each step, executes per the kind's semantics
4. Records pass/fail per step
5. Exits 0 on all-pass, 1 on first failure (or with `--continue`, exits 1 after all run)

## Authoring scenarios

A scenario should:

- Cover **one** behaviour cleanly. Compound scenarios are harder to debug.
- Use the standard token placeholders unless the test specifically needs custom ones.
- Reference bundles by id, not inline (keeps scenario YAMLs readable).
- Tag accurately — `required` is reserved for behaviours mandated by the spec; everything else is `recommended` or `extended`.
- Include `spec_refs` linking to the relevant spec section so a reader can audit conformance is right-shaped.

## Validating a scenario

```sh
lumencast validate-scenario conformance/v1/scenarios/<id>.yaml
```

Checks the YAML against the format described here. CI runs this on every change to `conformance/v1/scenarios/`.
