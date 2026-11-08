# MCP Conformance

The library is tested against the official MCP conformance suite,
[`@modelcontextprotocol/conformance`](https://github.com/modelcontextprotocol/conformance),
for every protocol revision it speaks: 2025-03-26, 2025-06-18, 2025-11-25 and
2026-07-28. CI runs it on every push and pull request (`.github/workflows/test.yml`).

## Running it

Needs Go and Node.js (for `npx`):

```bash
conformance/run.sh
```

That builds and starts the fixture server, runs the suite (`--suite all`) once
per revision against that revision's baseline, and stops the server. It exits
non-zero on any unexpected failure.

| Variable | Default | |
|---|---|---|
| `SPEC_VERSIONS` | all four revisions | e.g. `SPEC_VERSIONS=2026-07-28` |
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

[`expected-failures/`](expected-failures) lists, per revision, the scenarios
that fail because the library does not implement an optional capability.

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

The spec lets a server leave all of these out. Anything else that fails is a
regression. A listed scenario that starts passing also fails the run, so
remove its entry when a feature lands.
