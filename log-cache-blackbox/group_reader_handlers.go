package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	logcache "code.cloudfoundry.org/go-log-cache"
	"code.cloudfoundry.org/go-loggregator/rpc/loggregator_v2"
	uuid "github.com/nu7hatch/gouuid"
)

func groupLatencyHandler(cfg Config) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		size, err := strconv.Atoi(r.URL.Query().Get("size"))
		if err != nil {
			log.Printf("invalid size: %s %s", r.URL.Query().Get("size"), err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		groupUUID, err := uuid.NewV4()
		if err != nil {
			log.Printf("unable to create groupUUID: %s", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		groupName := groupUUID.String()

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		reader, err := buildGroupReader(ctx, size, groupName, cfg)
		if err != nil {
			log.Printf("Unable to create group reader: %s", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		resultData, err := measureLatency(reader, groupName)
		if err != nil {
			log.Printf("error getting result data: %s", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		w.Write(resultData)
		return
	})
}

func groupReliabilityHandler(cfg Config) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		size, err := strconv.Atoi(r.URL.Query().Get("size"))
		if err != nil {
			log.Printf("invalid size: %s %s", r.URL.Query().Get("size"), err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		groupUUID, err := uuid.NewV4()
		if err != nil {
			log.Printf("unable to create groupUUID: %s", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		groupName := groupUUID.String()

		// prime

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		reader, err := buildGroupReader(ctx, size, groupName, cfg)
		if err != nil {
			log.Printf("Unable to create group reader: %s", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		emitCount := 10000
		prefix := fmt.Sprintf("%d - ", time.Now().UnixNano())

		start := time.Now()
		go func() {
			// Give the system time to get the envelopes
			time.Sleep(20 * time.Second)

			for i := 0; i < emitCount; i++ {
				log.Printf("%s %d", prefix, i)
				time.Sleep(time.Millisecond)
			}
		}()

		var receivedCount int
		walkCtx, _ := context.WithTimeout(ctx, 40*time.Second)
		log.Printf("Starting walk...")
		logcache.Walk(
			walkCtx,
			groupName,
			func(envelopes []*loggregator_v2.Envelope) bool {
				for _, e := range envelopes {
					if strings.Contains(string(e.GetLog().GetPayload()), prefix) {
						receivedCount++
					}
				}
				return receivedCount < emitCount
			},
			reader,
			logcache.WithWalkStartTime(start),
			logcache.WithWalkBackoff(logcache.NewRetryBackoff(50*time.Millisecond, 100)),
			logcache.WithWalkLogger(log.New(os.Stderr, "[WALK]", 0)),
		)

		result := ReliabilityTestResult{
			LogsSent:     emitCount,
			LogsReceived: receivedCount,
		}

		resultData, err := json.Marshal(&result)
		if err != nil {
			log.Printf("failed to marshal test results: %s", err)
			return
		}

		w.Write(resultData)
	})
}

func buildGroupReader(ctx context.Context, size int, groupName string, cfg Config) (logcache.Reader, error) {
	httpClient := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: cfg.SkipSSLValidation},
		},
		Timeout: 5 * time.Second,
	}

	sIDs, err := sourceIDs(httpClient, cfg, size)
	if err != nil {
		return nil, fmt.Errorf("unable to get sourceIDs: %s", err)
	}

	client := logcache.NewShardGroupReaderClient(
		cfg.LogCacheAddr,
		logcache.WithHTTPClient(
			logcache.NewOauth2HTTPClient(
				cfg.UAAAddr,
				cfg.UAAClient,
				cfg.UAAClientSecret,
				logcache.WithOauth2HTTPClient(httpClient),
			),
		),
	)

	for _, sID := range sIDs {
		go func(sID string) {
			ticker := time.NewTicker(time.Second)
			for {
				ctx, _ := context.WithTimeout(ctx, 10*time.Second)
				err = client.SetShardGroup(ctx, groupName, sID)
				if err != nil {
					log.Printf("unable to set shard group: %s", err)
				}

				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					continue
				}
			}
		}(sID)
	}

	return client.BuildReader(rand.Uint64()), nil
}

func sourceIDs(httpClient *http.Client, cfg Config, size int) ([]string, error) {
	client := logcache.NewClient(
		cfg.LogCacheAddr,
		logcache.WithHTTPClient(
			logcache.NewOauth2HTTPClient(
				cfg.UAAAddr,
				cfg.UAAClient,
				cfg.UAAClientSecret,
				logcache.WithOauth2HTTPClient(httpClient),
			),
		),
	)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	meta, err := client.Meta(ctx)
	if err != nil {
		return nil, err
	}

	sourceIDs := make([]string, 0, size)
	for k := range meta {
		if k == cfg.VCapApp.ApplicationID {
			continue
		}
		if len(sourceIDs) < size-1 {
			sourceIDs = append(sourceIDs, k)
		}
	}
	sourceIDs = append(sourceIDs, cfg.VCapApp.ApplicationID)
	if len(sourceIDs) != size {
		log.Printf("Asked for %d source IDs but only found %d", size, len(sourceIDs))
	}
	return sourceIDs, nil
}