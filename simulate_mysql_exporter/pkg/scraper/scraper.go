package scraper

import (
	"github.com/prometheus/client_golang/prometheus"
)

//https://github.com/prometheus/mysqld_exporter/blob/master/collector/scraper.go

// CommonScraper 接口是抓取器的最小接口，可让我们向本 exporter 添加新的 Prometheus 指标。
// 可以这么理解，每个抓取 Metric 的行为，都会抽象成一个 **Scraper(抓取器)**。
// 并且，可以通过命令行标志来控制开启或关闭哪个抓取器
// 注意：抓取器的 Name和Help 与 Metric 的 Name和Help 不是一个概念
type CommonScraper interface {
	// Name 是抓取器的名称. Should be unique.
	Name() string

	// Help 是抓取器的帮助信息，这里的 Help 的内容将会作为命令行标志的帮助信息。
	Help() string

	// Scrape 是抓取器的具体行为。从客户端采集数据，并将其作为 Metric 通过 channel(通道) 发送。
	Scrape(client CommonClient, ch chan<- prometheus.Metric) error
}

// CommonClient 是连接 Server 的客户端接口，连接不同的 Server，实现不同。
// 用于为 CommonScraper 接口的 Scrape 方法提供连接 Server 所需的信息。
// 并且 Request 方法应该可以获取指定 API 下的 响应 Body，并提供给 CommonScraper.Scrape 用来处理这些信息，以便展示 Metrics
type CommonClient interface {
	Request(endpoint string) (body []byte, err error)
	Ping() (bool, error)
}
