# `pulse schema`

Print the JSON Schema (draft 2020-12) describing every public Pulse
request and response payload.

```bash
pulse schema
```

The output is a self-contained JSON Schema document — it carries its own
`$schema` and `$id` — so it is emitted **raw**, not wrapped in the
standard `--json` envelope. It is byte-identical to the file published at
the schema's `$id`
(<https://frankbardon.github.io/pulse/payload-schema.json>) and to the
`pulse://schema` MCP resource.

Typical use — validate a request body offline:

```bash
pulse schema > payload-schema.json
# validate ./request.json against payload-schema.json#/$defs/Request
# with any draft-2020-12 validator
```

See the [Payload Contract](../contract/payload-schema.md) chapter for the
schema's structure, the three access surfaces, how it stays in sync with
the engine, and its v1 strictness boundaries.
