package config

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/onebusaway/oba-validator/sink"
)

// DataSource is one operator's set of feeds plus its agency remap.
type DataSource struct {
	StaticGtfsFeedURL   string            `json:"staticGtfsFeedURL"`
	VehiclePositionsURL string            `json:"vehiclePositionsURL"`
	TripUpdatesURL      string            `json:"tripUpdatesURL"`
	ServiceAlertsURL    string            `json:"serviceAlertsURL"`
	AgencyMapping       map[string]string `json:"agencyMapping"`
}

// Config is the full validator configuration. Runtime-only fields (NoCache,
// Refresh) are set by the CLI, not normally present in the JSON.
type Config struct {
	OBAServerURL       string       `json:"obaServerURL"`
	APIKey             string       `json:"apiKey"`
	DataSources        []DataSource `json:"dataSources"`
	SampleSize         int          `json:"sampleSize"`
	RTFreshnessSeconds int          `json:"rtFreshnessSeconds"`
	LocationSpan       float64      `json:"locationSpan"`
	MaxConcurrency     int          `json:"maxConcurrency"`
	TimeoutSeconds     int          `json:"timeoutSeconds"`
	CacheDir           string       `json:"cacheDir"`

	// Result sink — optional. Activated when DBURL is non-blank; all five must
	// then be present together (see sink.Config.Validate). These are invocation
	// inputs from obacloud's ServerValidationJob, not user-facing config; do
	// not surface them in --help or error messages.
	DBURL         string `json:"db_url,omitempty"`
	DBUser        string `json:"db_user,omitempty"`
	DBPass        string `json:"db_pass,omitempty"`
	CorrelationID string `json:"correlation_id,omitempty"`
	ResultTable   string `json:"result_table,omitempty"`

	NoCache bool `json:"-"`
	Refresh bool `json:"-"`
}

// Load reads config from a file path or a raw JSON string (auto-detected by a
// leading '{'). Applies defaults and validates required fields.
func Load(pathOrJSON string) (Config, error) {
	var raw []byte
	if strings.HasPrefix(strings.TrimSpace(pathOrJSON), "{") {
		raw = []byte(pathOrJSON)
	} else {
		b, err := os.ReadFile(pathOrJSON)
		if err != nil {
			return Config{}, fmt.Errorf("reading config file: %w", err)
		}
		raw = b
	}
	var cfg Config
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return Config{}, fmt.Errorf("parsing config JSON: %w", err)
	}
	cfg.applyDefaults()
	if err := cfg.validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (c *Config) applyDefaults() {
	if c.APIKey == "" {
		c.APIKey = os.Getenv("ONEBUSAWAY_API_KEY")
	}
	if c.SampleSize == 0 {
		c.SampleSize = 3
	}
	if c.RTFreshnessSeconds == 0 {
		c.RTFreshnessSeconds = 300
	}
	if c.LocationSpan == 0 {
		c.LocationSpan = 0.01
	}
	if c.MaxConcurrency == 0 {
		c.MaxConcurrency = 4
	}
	if c.TimeoutSeconds == 0 {
		c.TimeoutSeconds = 120
	}
}

// SinkConfig assembles the optional result-sink configuration from the five
// flat invocation-input fields. The returned Config is value-copied, so
// downstream Write callers can hold it without aliasing config state.
func (c Config) SinkConfig() sink.Config {
	return sink.Config{
		DBURL:         c.DBURL,
		DBUser:        c.DBUser,
		DBPass:        c.DBPass,
		CorrelationID: c.CorrelationID,
		ResultTable:   c.ResultTable,
	}
}

func (c Config) validate() error {
	if c.OBAServerURL == "" {
		return fmt.Errorf("obaServerURL is required")
	}
	if c.APIKey == "" {
		return fmt.Errorf("apiKey is required (set in config or ONEBUSAWAY_API_KEY)")
	}
	if len(c.DataSources) == 0 {
		return fmt.Errorf("at least one dataSource is required")
	}
	if err := c.SinkConfig().Validate(); err != nil {
		return err
	}
	return nil
}
