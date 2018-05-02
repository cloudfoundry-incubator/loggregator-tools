package main

import (
	"encoding/json"
	"net/url"
)

type Config struct {
	LogCacheURL *url.URL `env:"LOG_CACHE_URL, required, report"`

	Port    int     `env:"PORT,             required, report"`
	VCapApp VCapApp `env:"VCAP_APPLICATION, required, report"`

	UAAAddr         string `env:"UAA_ADDR,          required, report"`
	UAAClient       string `env:"UAA_CLIENT,        required"`
	UAAClientSecret string `env:"UAA_CLIENT_SECRET, required"`

	SkipSSLValidation bool `env:"SKIP_SSL_VALIDATION, report"`
}

type VCapApp struct {
	ApplicationID string `json:"application_id"`
}

func (v *VCapApp) UnmarshalEnv(jsonData string) error {
	return json.Unmarshal([]byte(jsonData), &v)
}
