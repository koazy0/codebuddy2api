package config

type Gateway struct {
	APIKey          string       `mapstructure:"api-key" json:"api-key" yaml:"api-key"`
	AdminKey        string       `mapstructure:"admin-key" json:"admin-key" yaml:"admin-key"`
	Upstream        string       `mapstructure:"upstream" json:"upstream" yaml:"upstream"`
	Gzip            bool         `mapstructure:"gzip" json:"gzip" yaml:"gzip"`
	Passthrough     bool         `mapstructure:"passthrough" json:"passthrough" yaml:"passthrough"`
	InjectReasoning bool         `mapstructure:"inject-reasoning" json:"inject-reasoning" yaml:"inject-reasoning"`
	TimeoutSeconds  int          `mapstructure:"timeout-seconds" json:"timeout-seconds" yaml:"timeout-seconds"`
	MaxRetries      int          `mapstructure:"max-retries" json:"max-retries" yaml:"max-retries"`
	Rotate          string       `mapstructure:"rotate" json:"rotate" yaml:"rotate"`
	TrustEnvProxy   bool         `mapstructure:"trust-env-proxy" json:"trust-env-proxy" yaml:"trust-env-proxy"`
	Proxy           string       `mapstructure:"proxy" json:"proxy" yaml:"proxy"`
	ModelAlias      []ModelAlias `mapstructure:"model-alias" json:"model-alias" yaml:"model-alias"`
}

type ModelAlias struct {
	From string `mapstructure:"from" json:"from" yaml:"from"`
	To   string `mapstructure:"to" json:"to" yaml:"to"`
}

func (g Gateway) UpstreamBase() string {
	if g.Upstream == "" {
		return "https://copilot.tencent.com"
	}
	return g.Upstream
}

func (g Gateway) Timeout() int {
	if g.TimeoutSeconds <= 0 {
		return 300
	}
	return g.TimeoutSeconds
}

func (g Gateway) Retries() int {
	if g.MaxRetries <= 0 {
		return 3
	}
	return g.MaxRetries
}

type CodeBuddy struct {
	IDEType          string `mapstructure:"ide-type" json:"ide-type" yaml:"ide-type"`
	IDEVersion       string `mapstructure:"ide-version" json:"ide-version" yaml:"ide-version"`
	ProductVersion   string `mapstructure:"product-version" json:"product-version" yaml:"product-version"`
	Product          string `mapstructure:"product" json:"product" yaml:"product"`
	Domain           string `mapstructure:"domain" json:"domain" yaml:"domain"`
	EnvID            string `mapstructure:"env-id" json:"env-id" yaml:"env-id"`
	AgentIntent      string `mapstructure:"agent-intent" json:"agent-intent" yaml:"agent-intent"`
	DefaultMaxTokens int    `mapstructure:"default-max-tokens" json:"default-max-tokens" yaml:"default-max-tokens"`
	BillingBase      string `mapstructure:"billing-base" json:"billing-base" yaml:"billing-base"`
}

func (c CodeBuddy) HeaderIDEType() string {
	if c.IDEType == "" {
		return "CodeBuddyIDE"
	}
	return c.IDEType
}

func (c CodeBuddy) HeaderIDEVersion() string {
	if c.IDEVersion == "" {
		return "4.9.7"
	}
	return c.IDEVersion
}

func (c CodeBuddy) HeaderProductVersion() string {
	if c.ProductVersion != "" {
		return c.ProductVersion
	}
	return c.HeaderIDEVersion()
}

func (c CodeBuddy) HeaderProduct() string {
	if c.Product == "" {
		return "SaaS"
	}
	return c.Product
}

func (c CodeBuddy) HeaderDomain() string {
	if c.Domain == "" {
		return "www.codebuddy.cn"
	}
	return c.Domain
}

func (c CodeBuddy) HeaderEnvID() string {
	if c.EnvID == "" {
		return "production"
	}
	return c.EnvID
}

func (c CodeBuddy) HeaderAgentIntent() string {
	if c.AgentIntent == "" {
		return "craft"
	}
	return c.AgentIntent
}

func (c CodeBuddy) BillingBaseURL() string {
	if c.BillingBase == "" {
		return "https://www.codebuddy.cn"
	}
	return c.BillingBase
}

type Refresh struct {
	Enabled        bool   `mapstructure:"enabled" json:"enabled" yaml:"enabled"`
	Cron           string `mapstructure:"cron" json:"cron" yaml:"cron"`
	ThresholdDays  int    `mapstructure:"threshold-days" json:"threshold-days" yaml:"threshold-days"`
	TimeoutSeconds int    `mapstructure:"timeout-seconds" json:"timeout-seconds" yaml:"timeout-seconds"`
}

func (r Refresh) Spec() string {
	if r.Cron == "" {
		return "0 3 * * *"
	}
	return r.Cron
}

func (r Refresh) Threshold() int {
	if r.ThresholdDays <= 0 {
		return 30
	}
	return r.ThresholdDays
}

func (r Refresh) Timeout() int {
	if r.TimeoutSeconds <= 0 {
		return 15
	}
	return r.TimeoutSeconds
}

type Watchdog struct {
	Enabled         bool `mapstructure:"enabled" json:"enabled" yaml:"enabled"`
	IntervalSeconds int  `mapstructure:"interval-seconds" json:"interval-seconds" yaml:"interval-seconds"`
	FailThreshold   int  `mapstructure:"fail-threshold" json:"fail-threshold" yaml:"fail-threshold"`
	CooldownSeconds int  `mapstructure:"cooldown-seconds" json:"cooldown-seconds" yaml:"cooldown-seconds"`
	HealthCheck     bool `mapstructure:"health-check" json:"health-check" yaml:"health-check"`
	SyncCredit      bool `mapstructure:"sync-credit" json:"sync-credit" yaml:"sync-credit"`
}

func (w Watchdog) Interval() int {
	if w.IntervalSeconds <= 0 {
		return 300
	}
	return w.IntervalSeconds
}

func (w Watchdog) FailLimit() int {
	if w.FailThreshold <= 0 {
		return 3
	}
	return w.FailThreshold
}

func (w Watchdog) Cooldown() int {
	if w.CooldownSeconds <= 0 {
		return 600
	}
	return w.CooldownSeconds
}
