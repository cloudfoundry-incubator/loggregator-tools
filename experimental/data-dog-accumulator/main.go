package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	datadog "github.com/zorkian/go-datadog-api"

	envstruct "code.cloudfoundry.org/go-envstruct"
	logcache "code.cloudfoundry.org/go-log-cache"
	"code.cloudfoundry.org/go-loggregator/rpc/loggregator_v2"
)

func main() {
	log.Print("Starting DataDog Accumulator...")
	defer log.Print("Closing DataDog Accumulator.")

	cfg := loadConfig()

	log.Printf("Scraping data for %s", cfg.SourceID)

	tr := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: cfg.SkipSSLValidation},
	}

	llc := logcache.NewClient(cfg.LogCacheAddr,
		logcache.WithHTTPClient(logcache.NewOauth2HTTPClient(
			cfg.UAAAddr,
			cfg.UAAClient,
			cfg.UAAClientSecret,
			logcache.WithOauth2HTTPClient(
				&http.Client{
					Transport: tr,
					Timeout:   5 * time.Second,
				},
			))),
	)

	ddc := datadog.NewClient(cfg.DataDogAPIKey, "")

	logcache.Walk(
		context.Background(),
		cfg.SourceID,
		buildDataDogWriter(ddc, cfg.Prefix, cfg.Host),
		llc.Read,
		logcache.WithWalkBackoff(logcache.NewAlwaysRetryBackoff(time.Second)),
		logcache.WithWalkLogger(log.New(os.Stderr, "", 0)),
		logcache.WithWalkStartTime(time.Now().Add(-10*time.Minute)),
	)
}

func buildDataDogWriter(ddc *datadog.Client, prefix, host string) func([]*loggregator_v2.Envelope) bool {
	return func(es []*loggregator_v2.Envelope) bool {
		for _, e := range es {
			switch e.Message.(type) {
			case *loggregator_v2.Envelope_Gauge:
				for name, value := range e.GetGauge().Metrics {
					// We plan to take the address of this and therefore can not
					// use name given to us via range.
					name := name
					if prefix != "" {
						name = fmt.Sprintf("%s.%s", prefix, name)
					}

					mType := "gauge"
					metric := datadog.Metric{
						Metric: &name,
						Points: toDataPoint(e.Timestamp, value.GetValue()),
						Type:   &mType,
						Host:   &host,
					}

					log.Printf("Posting %s: %v", name, value.GetValue())

					err := ddc.PostMetrics([]datadog.Metric{metric})

					if err != nil {
						log.Printf("failed to write metrics to DataDog: %s", err)
					}
				}
			case *loggregator_v2.Envelope_Counter:
				name := e.GetCounter().GetName()
				if prefix != "" {
					name = fmt.Sprintf("%s.%s", prefix, name)
				}

				mType := "gauge"
				metric := datadog.Metric{
					Metric: &name,
					Points: toDataPoint(e.Timestamp, float64(e.GetCounter().GetTotal())),
					Type:   &mType,
					Host:   &host,
				}

				log.Printf("Posting %s: %v", name, e.GetCounter().GetTotal())

				err := ddc.PostMetrics([]datadog.Metric{metric})

				if err != nil {
					log.Printf("failed to write metrics to DataDog: %s", err)
				}
			default:
				continue
			}
		}
		return true
	}
}

func toDataPoint(x int64, y float64) []datadog.DataPoint {
	t := time.Unix(0, x)
	tf := float64(t.Unix())
	return []datadog.DataPoint{
		[2]*float64{&tf, &y},
	}
}

type Config struct {
	LogCacheAddr  string `env:"LOG_CACHE_ADDR,required"`
	SourceID      string `env:"SOURCE_ID,required"`
	DataDogAPIKey string `env:"DATA_DOG_API_KEY,required"`
	Host          string `env:"HOST,required"`
	Prefix        string `env:"METRIC_PREFIX"`

	UAAAddr         string `env:"UAA_ADDR,          required"`
	UAAClient       string `env:"UAA_CLIENT,        required"`
	UAAClientSecret string `env:"UAA_CLIENT_SECRET, required"`

	SkipSSLValidation bool `env:"SKIP_SSL_VALIDATION"`
}

func loadConfig() *Config {
	var cfg Config
	if err := envstruct.Load(&cfg); err != nil {
		log.Fatalf("failed to load config: %s", err)
	}

	return &cfg
}