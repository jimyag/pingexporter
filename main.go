package main

import (
	"fmt"
	"net/http"
	"os"

	"github.com/BurntSushi/toml"
	"github.com/jimyag/log"
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

	// 创建HTTP处理程序，用于暴露指标
	http.Handle("/metrics", promhttp.Handler())

	// 启动HTTP服务
	go func() {
		log.Info().Str("address", cfg.Web.Address).Msg("starting web server")
		if err := http.ListenAndServe(cfg.Web.Address, nil); err != nil {
			fmt.Println(err)
		}
	}()

	// 保持程序运行
	select {}
}
