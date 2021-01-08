package collector

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"

	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	log "github.com/sirupsen/logrus"
)

const (
	name      = "xsky_exporter"
	namespace = "xsky"
	//Subsystem(s).
	exporter = "exporter"
)

// Name is
func Name() string {
	return name
}

// Verify if Exporter implements prometheus.Collector
var _ prometheus.Collector = (*Exporter)(nil)

// Metric descriptors.
var (
	scrapeDurationDesc = prometheus.NewDesc(
		prometheus.BuildFQName(namespace, exporter, "collector_duration_seconds"),
		"Collector time duration.",
		[]string{"collector"}, nil,
	)
)

// Exporter 实现了 prometheus.Collector，其中包含了很多 Metric。
// 只要 Exporter 实现了 prometheus.Collector，就可以调用 MustRegister() 将其注册到 prometheus 库中
type Exporter struct {
	//ctx      context.Context  //http timeout will work, don't need this
	client   *XskyClient
	scrapers []Scraper
	metrics  Metrics
}

// NewExporter 实例化 Exporter
func NewExporter(opts *XskyOpts, metrics Metrics, scrapers []Scraper) (*Exporter, error) {
	uri := opts.URL
	if !strings.Contains(uri, "://") {
		uri = "http://" + uri
	}
	u, err := url.Parse(uri)
	if err != nil {
		return nil, fmt.Errorf("invalid Xsky URL: %s", err)
	}
	if u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, fmt.Errorf("invalid Xsky URL: %s", uri)
	}

	// ######## 配置 http.Client 的信息 ########
	rootCAs, err := x509.SystemCertPool()
	// if err != nil {
	// 	return nil, err
	// }
	// 初始化 TLS 相关配置信息
	tlsClientConfig := &tls.Config{
		MinVersion: tls.VersionTLS12,
		RootCAs:    rootCAs,
	}
	// 可以通过命令行选项配置 TLS 的 InsecureSkipVerify
	// 这个配置决定是否跳过 https 协议的验证过程，就是 curl 加不加 -k 选项。默认跳过
	if opts.Insecure {
		tlsClientConfig.InsecureSkipVerify = true
	}
	transport := &http.Transport{
		TLSClientConfig: tlsClientConfig,
	}
	xc := &XskyClient{
		Opts: opts,
		Client: &http.Client{
			Timeout:   opts.Timeout,
			Transport: transport,
		},
	}
	// ######## 配置 http.Client 信息结束 ########

	return &Exporter{
		client:   xc,
		metrics:  metrics,
		scrapers: scrapers,
	}, nil
}

// Describe 实现 Collector 接口的方法
func (e *Exporter) Describe(ch chan<- *prometheus.Desc) {
	ch <- e.metrics.TotalScrapes.Desc()
	e.metrics.ScrapeErrors.Describe(ch)
	ch <- e.metrics.Error.Desc()
	ch <- e.metrics.XskyUP.Desc()
}

// Collect 实现 Collector 接口的方法
func (e *Exporter) Collect(ch chan<- prometheus.Metric) {
	// 将 scrape() 方法引进来，用来在实现 Collect 接口后，调用 prometheus 功能可以操作 scrape() 中相关的 Metrics
	e.scrape(ch)

	ch <- e.metrics.TotalScrapes
	e.metrics.ScrapeErrors.Collect(ch)
	ch <- e.metrics.Error
	ch <- e.metrics.XskyUP
}

// scrape 调用每个已经注册的 Scraper(抓取器) 执行其代码中定义的抓取行为。
func (e *Exporter) scrape(ch chan<- prometheus.Metric) {
	e.metrics.TotalScrapes.Inc()

	// 第一个 scrapeTime,开始统计 scrape 指标的耗时
	scrapeTime := time.Now()

	// 检验目标服务器是否正常，每次执行 Collect 都会检查
	if pong, err := e.client.Ping(); pong != true || err != nil {
		log.WithFields(log.Fields{
			"url":      e.client.Opts.URL + "/configurations",
			"username": e.client.Opts.Username,
		}).Error(err)
		e.metrics.XskyUP.Set(0)
		e.metrics.Error.Set(1)
	}
	e.metrics.XskyUP.Set(1)
	e.metrics.Error.Set(0)

	// 对应第一个 scrapeTime，scrapeDurationDesc 这个 Metric 用于显示抓取标签为 reach 指标所消耗的时间
	ch <- prometheus.MustNewConstMetric(scrapeDurationDesc, prometheus.GaugeValue, time.Since(scrapeTime).Seconds(), "reach")

	var wg sync.WaitGroup
	defer wg.Wait()

	// ！！！！！！！！！！！！！！！！！！！！！！！！！！！！！！！！！！！
	// 本代码中最核心的执行部分，通过一个 for 循环来执行所有经注册的 Scraper
	// ！！！！！！！！！！！！！！！！！！！！！！！！！！！！！！！！！！！
	// 由于所有自定义的 Scrapers 都实现了 Scraper 接口，所以这里的 e.scrapers 其实是那些 抓取器 结构体的集合
	for _, scraper := range e.scrapers {
		wg.Add(1)
		// go 协程，同时执行所有 Scraper
		go func(scraper Scraper) {
			defer wg.Done()
			// 第二个 scrapeTime,开始统计 scrape 指标的耗时
			label := scraper.Name()
			scrapeTime := time.Now()
			// 执行 Scrape 操作，也就是执行每个 Scraper 中的 Scrape() 方法，由于这些自定义的 Scraper 都实现了 Scraper 接口
			// 所以 scraper.Scrape 这个调用，就是调用的当前循环体中，从 e.scrapers 数组中取到的值，也就是 collector.ScrapeCluster{} 这些结构体
			if err := scraper.Scrape(e.client, ch); err != nil {
				log.WithField("scraper", scraper.Name()).Error(err)
				e.metrics.ScrapeErrors.WithLabelValues(label).Inc()
				e.metrics.Error.Set(1)
			}
			// 对应第二个 scrapeTime，scrapeDurationDesc 这个 Metric，用于显示抓取标签为 label(这是变量) 指标所消耗的时间
			// 其实就是统计每个 Scraper 执行所消耗的时间
			ch <- prometheus.MustNewConstMetric(scrapeDurationDesc, prometheus.GaugeValue, time.Since(scrapeTime).Seconds(), label)
		}(scraper)
	}
}

// Metrics 本程序默认自带的一些 Metrics
type Metrics struct {
	TotalScrapes prometheus.Counter
	ScrapeErrors *prometheus.CounterVec
	Error        prometheus.Gauge
	XskyUP       prometheus.Gauge
}

// NewMetrics 实例化 Metrics，设定本程序默认自带的一些 Metrics 的信息
func NewMetrics() Metrics {
	subsystem := exporter
	return Metrics{
		TotalScrapes: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: namespace,
			Subsystem: subsystem,
			Name:      "scrapes_total",
			Help:      "Total number of times Xsky was scraped for metrics.",
		}),
		ScrapeErrors: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace,
			Subsystem: subsystem,
			Name:      "scrape_errors_total",
			Help:      "Total number of times an error occurred scraping a Xsky.",
		}, []string{"collector"}),
		Error: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: namespace,
			Subsystem: subsystem,
			Name:      "last_scrape_error",
			Help:      "Whether the last scrape of metrics from Xsky resulted in an error (1 for error, 0 for success).",
		}),
		XskyUP: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: namespace,
			Name:      "up",
			Help:      "Whether the Xsky is up.",
		}),
	}
}
