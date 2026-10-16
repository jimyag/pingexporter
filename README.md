# pingexporter

Pingexporter is a project that allows you to monitor the availability and latency of network hosts using ICMP ping requests.

It collects ping metrics and generates detailed reports for analysis.

This tool draws inspiration from both the [ping_exporter](https://github.com/czerwonk/ping_exporter) and [SmokePing](https://github.com/oetiker/SmokePing) repositories, combining their strengths and introducing additional enhancements.

## install

```bash
go get github.com/jimyag/pingexporter@latest
```

or via [github release](https://github.com/jimyag/pingexporter/releases)

## usage

```bash
pingexporter config.toml
```

```bash
curl http://localhost:9113/metrics

```

## config

see [config file](config.toml) for more details

DNS targets are resolved at startup and refreshed every minute by default.
Set `dns.refresh` to a duration such as `"30s"` to change the interval, or a
negative duration such as `"-1s"` to disable refresh. Address changes add new
probes and remove obsolete ones without restarting probes whose addresses
have not changed. Failed or empty DNS responses keep the last resolved targets
until a later refresh succeeds. DNS lookups time out after five seconds.

Configuration is checked before opening ICMP sockets. Ping intervals, timeouts
and history sizes must be positive. Target addresses must be nonempty and unique.
Label names must be valid Prometheus label names, must not start with `__`, and
must not use the built-in names `addr`, `ip` or `version`. Target labels must not
reuse global label names.

## DNS metrics

For each hostname target, `ping_exporter_dns_lookup_success` is 1 when the latest
lookup returned addresses and 0 after a failed or empty response.
`ping_exporter_dns_last_success_timestamp_seconds` records the last successful
lookup time, or 0 if none has succeeded. Both metrics include `addr`, global
labels and target labels. Literal IP targets do not emit DNS metrics.

The `version` label on ping metrics is `v4` for IPv4 and `v6` for IPv6.
Earlier releases had these values reversed; update any queries that compensated
for the old behavior when upgrading.

## Development checks

```bash
go test -race ./...
go build ./...
```

The integration test runs the exporter as a subprocess with a local DNS server
and checks `/metrics` through DNS failures and address changes. On Linux, run it
with raw ICMP socket permissions:

```bash
go test -c -race -tags=integration -o /tmp/pingexporter-integration.test .
sudo /tmp/pingexporter-integration.test -test.v -test.timeout=90s
```

Branch pushes and pull requests run these checks. Tag releases must pass the
same checks before publishing binaries.
