package main

import (
	"net/http"
	"os"

	"github.com/BurntSushi/toml"
	"github.com/jimyag/log"
	_ "github.com/jimyag/version-go"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/jimyag/pingexporter/config"
	"github.com/jimyag/pingexporter/metrics"
)

func main() {
	if len(os.Args) < 2 {
		log.Error().Msg("usage is: ping_exporter <config-path>")
		os.Exit(1)
	}
	configFile := &os.Args[1]
	cfg := &config.Config{}
	cfg.GlobalLabels = make(map[string]string)
	if _, err := toml.DecodeFile(*configFile, &cfg); err != nil {
		log.Panic(err).Msg("could not load config")
	}
	log.Info().Any("config", cfg).Msg("loaded config")
	cfg.Default()
	cfg.Verify()
	ping := metrics.New(cfg)
	prometheus.MustRegister(ping)
	http.Handle(cfg.Web.MetricsPath, promhttp.Handler())

	log.Info().Str("address", cfg.Web.Address).Msg("starting web server")
	if err := http.ListenAndServe(cfg.Web.Address, nil); err != nil {
		log.Panic(err).Msg("listen failed")
	}
}
