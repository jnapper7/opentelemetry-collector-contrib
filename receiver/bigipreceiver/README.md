# F5 Big-IP Receiver

| Status                   |         |
| ------------------------ |---------|
| Stability                | [beta]  |
| Supported pipeline types | metrics |
| Distributions            | none    |

This receiver fetches stats from a F5 Big-IP node using F5's [iControl REST API](https://clouddocs.f5.com/api/icontrol-rest).

## Prerequisites

This receiver supports Big-IP versions `11.6.5+`

## Configuration

The following settings are required:
- `username`
- `password`

The following settings are optional:

- `endpoint` (default: `https://localhost:443`): The URL of the Big-IP environment.
- `collection_interval` (default = `10s`): This receiver collects metrics on an interval. Valid time units are `ns`, `us` (or `µs`), `ms`, `s`, `m`, `h`.
- `tls` (defaults defined [here](https://github.com/open-telemetry/opentelemetry-collector/blob/main/config/configtls/README.md)): TLS control. By default insecure settings are rejected and certificate verification is on.

### Example Configuration

```yaml
receivers:
  bigip:
    collection_interval: 10s
    endpoint: https://localhost:443
    username: otelu
    password: $BIGIP_PASSWORD
    tls:
      insecure_skip_verify: true
```

The full list of settings exposed for this receiver are documented [here](./config.go) with detailed sample configurations [here](./testdata/config.yaml). TLS config is documented further under the [opentelemetry collector's configtls package](https://github.com/open-telemetry/opentelemetry-collector/blob/main/config/configtls/README.md).

## Metrics

Details about the metrics produced by this receiver can be found in [documentation.md](./documentation.md)

[beta]: https://github.com/open-telemetry/opentelemetry-collector#beta
