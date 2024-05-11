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
