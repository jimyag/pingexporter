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
