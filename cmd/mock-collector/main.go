package main

import (
	"context"
	"fmt"
	"github.com/go-kit/log/level"
	"github.com/oklog/run"
	commonconfig "github.com/prometheus/common/config"
	"github.com/prometheus/prometheus/discovery/targetgroup"
	"github.com/prometheus/prometheus/storage"
	"io/ioutil"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/go-kit/log"
	"github.com/open-telemetry/opentelemetry-collector-contrib/receiver/prometheusreceiver/targetallocator"
	"github.com/prometheus/client_golang/prometheus"
	promconfig "github.com/prometheus/prometheus/config"
	"github.com/prometheus/prometheus/discovery"
	"github.com/prometheus/prometheus/scrape"
	"go.opentelemetry.io/collector/component/componenttest"
	"go.opentelemetry.io/collector/receiver/receivertest"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// const TA_ENDPOINT = "http://cloudwatch-agent-targetallocator:80"
const TA_ENDPOINT = "http://localhost:8080"
const COLLECTOR_ID = "cloudwatch-agent-0"

type App struct {
	storage.Appendable
}

func initPrometheusManagers(ctx context.Context, configLoaded chan struct{}, scraperChan chan map[string][]*targetgroup.Group) (*scrape.Manager, *discovery.Manager) {
	w := log.NewSyncWriter(os.Stdout)
	logger := log.NewLogfmtLogger(w)
	logger = level.NewFilter(logger, level.AllowDebug())
	reg := prometheus.NewRegistry()
	sdMetrics, err := discovery.RegisterSDMetrics(reg, discovery.NewRefreshMetrics(reg))
	if err != nil {
		level.Error(logger).Log(err)
		return nil, nil
	}
	//dm
	discoveryManager := discovery.NewManager(ctx, log.With(logger, "component", "discovery manager scrape"), reg, sdMetrics)
	go func() {
		level.Info(logger).Log("msg", "Starting discovery manager")
		if err = discoveryManager.Run(); err != nil {
			level.Info(logger).Log("Discovery manager failed", zap.Error(err))
		}
	}()
	opts := &scrape.Options{
		PassMetadataInContext: true,
		ExtraMetrics:          true,
		HTTPClientOptions:     []commonconfig.HTTPClientOption{commonconfig.WithUserAgent(COLLECTOR_ID)},
	}

	scrapeManager, err := scrape.NewManager(opts, log.With(logger, "component", "scrape manager"), nil, reg)
	go func() {
		// The scrape manager needs to wait for the configuration to be loaded before beginning
		<-configLoaded
		level.Info(logger).Log("msg", "Starting scrape manager")
		if err := scrapeManager.Run(scraperChan); err != nil {
			level.Info(logger).Log("Scrape manager failed", zap.Error(err))
		}
	}()
	if err != nil {
		level.Error(logger).Log(err)
		return nil, discoveryManager
	}
	return scrapeManager, discoveryManager
}

func displayScrapePools(logger *zap.Logger, scrapeManager *scrape.Manager) {
	logger.Info(fmt.Sprintf("Scrape Pool: %+v", scrapeManager.ScrapePools()))
	logger.Info(fmt.Sprintf("Targets All %+v", scrapeManager.TargetsAll()))
	logger.Info(fmt.Sprintf("Targets Dropped %+v", scrapeManager.TargetsDropped()))
}
func showTestSyncChan(logger *zap.Logger, syncChDiscovery <-chan map[string][]*targetgroup.Group, syncChScraper chan map[string][]*targetgroup.Group) {
	for {
		select {
		case targetGroup := <-syncChDiscovery:
			logger.Info("Channel Log:", zap.Any("sync channel", targetGroup))
			syncChScraper <- targetGroup
		}
	}
}
func readyCheck(logger *zap.Logger) {
	res, err := http.Get(fmt.Sprintf("%s/readyz", TA_ENDPOINT))
	if err != nil {
		logger.Error("Ready failed", zap.Error(err))
		return
	}
	logger.Info("Ready check run", zap.String("status code", res.Status))

}
func scrapeConfig(logger *zap.Logger) {
	res, err := http.Get(fmt.Sprintf("%s/scrape_configs", TA_ENDPOINT))
	if err != nil {
		logger.Error("Ready failed", zap.Error(err))
		return
	}
	if res == nil {
		logger.Error("Response empty")
		return
	}
	if res.Body != nil {
		defer res.Body.Close()
	}

	body, readErr := ioutil.ReadAll(res.Body)
	if readErr != nil {
		logger.Error("Ready failed", zap.Error(readErr), zap.String("response", res.Status))
		return
	}
	logger.Info("Scrape config", zap.String("scrape config", string(body)))

}
func main() {
	ctx, cancelScrape := context.WithCancel(context.Background())
	cfg := targetallocator.Config{}
	cfg.Endpoint = TA_ENDPOINT
	cfg.Interval = 2 * time.Second
	cfg.CollectorID = COLLECTOR_ID

	zapConfig := zap.NewProductionConfig()
	zapConfig.Level = zap.NewAtomicLevelAt(zap.InfoLevel)
	zapConfig.EncoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder
	logger, err := zapConfig.Build()
	if err != nil {
		logger.Error("Failed to create zap logger", zap.Error(err))
		return
	}
	defer logger.Sync()
	logger.Info("Starting Mock Collector...")
	baseCfg := promconfig.Config{GlobalConfig: promconfig.DefaultGlobalConfig}
	targetAllocatorManagerReady := make(chan struct{})
	scraperCh := make(chan map[string][]*targetgroup.Group)
	scrapeManager, discoveryManager := initPrometheusManagers(ctx, targetAllocatorManagerReady, scraperCh)
	receiverSettings := receivertest.NewNopSettings()
	receiverSettings.Logger = logger

	manager := targetallocator.NewManager(receiverSettings, &cfg, &baseCfg, false)
	// time.Sleep(1 * time.Minute) // give TA time to boot up
	var wg *sync.WaitGroup

	var g run.Group
	{
		g.Add(
			func() error {
				logger.Info("Start TA Manager")
				err = manager.Start(ctx, componenttest.NewNopHost(), scrapeManager, discoveryManager)
				close(targetAllocatorManagerReady)
				for {
				}
				logger.Error("Mock Collector stopped", zap.Error(err))
				return err
			},
			func(err error) {
				manager.Shutdown()
				logger.Error("Stopping mock collector", zap.Error(err))
				cancelScrape()
			},
		)
	}
	{
		g.Add(
			func() error {
				<-targetAllocatorManagerReady
				logger.Info("RunTest")
				go showTestSyncChan(logger, discoveryManager.SyncCh(), scraperCh)
				for {
					displayScrapePools(logger, scrapeManager)
					scrapeConfig(logger)
					readyCheck(logger)
					time.Sleep(1 * time.Minute)
				}
				logger.Info("Test ended")
				return nil

			},
			func(err error) {

			},
		)
	}

	if err := g.Run(); err != nil {
		logger.Error("Run Group failed", zap.Error(err))
	}
	logger.Error("See you next time!")
	wg.Done()
}
