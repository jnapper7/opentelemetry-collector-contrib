# APIClarity HTTP Exporter

| Status                   |                       |
| ------------------------ | --------------------- |
| Stability                | traces [stable]       |
| Supported pipeline types | traces |
| Distributions            | [contrib]     |

Exports traces and/or metrics via HTTP to an [APIClarity](
https://github.com/openclarity/apiclarity/blob/master/plugins/api/swagger.yaml)
endpoint for analysis.

The following settings are required:

- `endpoint` (no default): The target base URL to send data to (e.g.: https://example.com:4318).
  The trace signal will be added to this base URL, i.e. "/api/telemetry" will be appended. 

The following settings can be optionally configured:

- `traces_endpoint` (no default): The target URL to send trace data to (e.g.: https://example.com:4318/v1/traces).
   If this setting is present the `endpoint` setting is ignored for traces.
- `tls`: see [TLS Configuration Settings](../../config/configtls/README.md) for the full set of available options.
- `timeout` (default = 30s): HTTP request time limit. For details see https://golang.org/pkg/net/http/#Client
- `read_buffer_size` (default = 0): ReadBufferSize for HTTP client.
- `write_buffer_size` (default = 512 * 1024): WriteBufferSize for HTTP client.

Example:

```yaml
exporters:
  apiclarity:
    endpoint: https://example.com:4318/api/telemetry
```

Note: the only supported (and default) compression is `none`

The full list of settings exposed for this exporter are documented [here](./config.go)
with detailed sample configurations [here](./testdata/config.yaml).

[contrib]: https://github.com/open-telemetry/opentelemetry-collector-releases/tree/main/distributions/otelcol-contrib
