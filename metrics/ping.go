package metrics

import (
	"sort"
	"strings"

	mon "github.com/digineo/go-ping/monitor"
	"github.com/jimyag/log"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/jimyag/pingexporter/config"
)

type Ping struct {
	monitor    *mon.Monitor
	cfg        *config.Config
	globalKey  []string
	metrics    map[string]*mon.Metrics
	bestDesc   *prometheus.Desc
	worstDesc  *prometheus.Desc
	meanDesc   *prometheus.Desc
	stdDevDesc *prometheus.Desc
	lossDesc   *prometheus.Desc //package lost / packets send
}

func New(cfg *config.Config) *Ping {
	m, err := cfg.GenMonitor()
	if err != nil {
		log.Panic(err).Msg("gen monitor")
	}
	p := &Ping{
		monitor: m,
		cfg:     cfg,
	}
	p.init()
	return p
}

func (p *Ping) Collect(ch chan<- prometheus.Metric) {
	if m := p.monitor.Export(); len(m) > 0 {
		p.metrics = m
	}
	for target, metrics := range p.metrics {
		// target ip version
		l := strings.Split(target, " ")
		if p.globalKey != nil {
			for _, k := range p.globalKey {
				l = append(l, p.cfg.GlobalLabels[k])
			}
		}
		if metrics.PacketsSent > metrics.PacketsLost {
			ch <- prometheus.MustNewConstMetric(p.bestDesc, prometheus.GaugeValue, float64(metrics.Best), l...)
			ch <- prometheus.MustNewConstMetric(p.worstDesc, prometheus.GaugeValue, float64(metrics.Worst), l...)
			ch <- prometheus.MustNewConstMetric(p.meanDesc, prometheus.GaugeValue, float64(metrics.Mean), l...)
			ch <- prometheus.MustNewConstMetric(p.stdDevDesc, prometheus.GaugeValue, float64(metrics.StdDev), l...)
		}
		loss := float64(metrics.PacketsLost) / float64(metrics.PacketsSent)
		ch <- prometheus.MustNewConstMetric(p.lossDesc, prometheus.GaugeValue, loss, l...)
	}
}

func (p *Ping) Describe(ch chan<- *prometheus.Desc) {
	ch <- p.bestDesc
	ch <- p.worstDesc
	ch <- p.meanDesc
	ch <- p.stdDevDesc
	ch <- p.lossDesc
}

func (p *Ping) init() {
	label := []string{"target", "ip", "version"}
	if p.cfg.GlobalLabels != nil {
		p.globalKey = make([]string, 0, len(p.cfg.GlobalLabels))
		for k := range p.cfg.GlobalLabels {
			p.globalKey = append(p.globalKey, k)
		}
		sort.Strings(p.globalKey)
		label = append(label, p.globalKey...)
	}
	p.bestDesc = newDesc("rtt_best", "best round trip time", label, nil)
	p.worstDesc = newDesc("rtt_worst", "worst round trip time", label, nil)
	p.meanDesc = newDesc("rtt_mean", "mean round trip time", label, nil)
	p.stdDevDesc = newDesc("rtt_std_dev", "standard deviation of round trip time", label, nil)
	p.lossDesc = newDesc("loss_ratio", "packets lost / packets sent", label, nil)
}
func newDesc(name, help string, variableLabels []string, constLabels prometheus.Labels) *prometheus.Desc {
	return prometheus.NewDesc("ping_exporter_"+name, help, variableLabels, constLabels)
}
