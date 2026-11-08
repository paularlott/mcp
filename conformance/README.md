# MCP Conformance

The library is tested against the official MCP conformance suite,
[`@modelcontextprotocol/conformance`](https://github.com/modelcontextprotocol/conformance),
for every protocol revision it speaks: 2025-03-26, 2025-06-18, 2025-11-25 and
2026-07-28, as a server and as a client. CI runs it on every push and pull
request (`.github/workflows/test.yml`).

## Running it

Needs Go and Node.js (for `npx`):

```bash
conformance/run.sh
```

That builds the fixture server and client, runs the suite's server scenarios
against the server and its client scenarios against the client (`--suite all`),
once per revision against that revision's baseline, and exits non-zero on any
unexpected failure.

| Variable | Default | |
|---|---|---|
| `SPEC_VERSIONS` | all four revisions | e.g. `SPEC_VERSIONS=2026-07-28` |
| `SUITES` | `server client` | run only one with `SUITES=server` or `SUITES=client` |
| `CONFORMANCE_VERSION` | `0.2.0-alpha.12` | suite version (the pinned one covers 2026-07-28) |
| `PORT` | `3001` | the server listens on `localhost:$PORT/mcp` |
| `OUTPUT_DIR` | a temporary folder | where per-scenario `checks.json` results go |

To run a single scenario against a server you started yourself:

```bash
go run ./conformance/server -addr localhost:3001
```

```bash
npx -y @modelcontextprotocol/conformance@0.2.0-alpha.12 server --url http://localhost:3001/mcp --spec-version 2026-07-28 --scenario tools-call-error
```

And a single client scenario (the suite starts its own mock server and runs
the client against it):

```bash
go build -o /tmp/mcp-conformance-client ./conformance/client
```

```bash
npx -y @modelcontextprotocol/conformance@0.2.0-alpha.12 client --command /tmp/mcp-conformance-client --spec-version 2026-07-28 --scenario http-custom-headers
```

## The fixture client

[`client/main.go`](client/main.go) is what the suite runs for each client
scenario: it gets the scenario's server URL as its last argument and the
scenario name in `MCP_CONFORMANCE_SCENARIO`, drives the library's `Client`
(connect, list tools, call what the scenario expects, send the extra requests
some scenarios check) and exits 0 or 1. The suite judges the client by what
its mock server observes: version negotiation and `_meta`, the 2026-07-28
`Mcp-Method`/`Mcp-Name`/`Mcp-Param-*` headers, dropping tools with invalid
`x-mcp-header` annotations, JSON Schema preservation and `$ref` handling, SSE
retry and resumption, and the authorization flows. For those it runs the
`Client` with an `AuthorizationCodeAuth` whose `Authorizer` is headless: the
suite's mock authorization server approves at once, so it requests the
authorization URL and returns the redirect. The extension `auth/*` scenarios
(client credentials, enterprise-managed authorization, DPoP, workload
identity) are not part of `--suite all` and are not run.

## The fixture server

[`server/main.go`](server/main.go) serves, on one endpoint and with only the
library's public API, the tools, resources and prompts the suite's scenarios
call by name (`test_simple_text`, `test://static-text`, `test_simple_prompt`,
…), completers for `test_prompt_with_arguments` and `test://template/{id}/data`,
the elicitation tools and prompt (`test_elicitation`, the SEP-1034 and SEP-1330
variants, the `test_input_required_result_*` fixtures; in
[`server/elicitation.go`](server/elicitation.go)), plus the diagnostic tools the
2026-07-28 scenarios probe with (`test_missing_capability`, `test_headers`, the
list-change triggers). Every
scenario also checks that each message the server sends is valid against the
spec's JSON Schema for that revision.

## Expected failures

[`expected-failures/`](expected-failures) lists, per revision, the server
(`server:`) and client (`client:`) scenarios that fail because the library
does not implement an optional capability.

### Server

Not implemented on purpose — 2026-07-28 deprecates these (SEP-2577) and says
new implementations SHOULD NOT adopt them:

- the logging utility (`logging/setLevel`, `notifications/message`); log to
  stderr or OpenTelemetry instead
- sampling (`sampling/createMessage`); call LLM provider APIs directly
- roots (`roots/list`); pass paths as tool arguments, resource URIs or
  configuration

Two 2026-07-28 scenarios fail for the same reason even though elicitation
works: `input-required-result-multiple-input-requests` wants elicitation,
sampling and roots input requests in one result, and
`input-required-result-capability-check` wants a sampling input request for a
client that declares only sampling.

Not implemented yet:

- URL-mode elicitation (2025-11-25 only; 2026-07-28 forbids it), which no
  scenario exercises

The spec lets a server leave all of these out.

### Client

Not implemented yet:

- answering elicitation in the `Client`: `elicitation/create` requests through
  2025-11-25 (`elicitation-sep1034-client-defaults`), and
  `InputRequiredResult` retries that echo `requestState` on 2026-07-28
  (`sep-2322-client-request-state`)

Anything else that fails is a regression. A listed scenario that starts
passing also fails the run, so remove its entry when a feature lands.
