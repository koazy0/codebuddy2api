package service

var (
	DefaultClient    *UpstreamClient
	DefaultRotator   *Rotator
	DefaultRefresher *Refresher
	DefaultWatchdog  *Watchdog
	DefaultProxy     *Proxy
)

func InitRuntime() {
	DefaultClient = NewUpstreamClient()
	DefaultRotator = NewRotator()
	DefaultRefresher = NewRefresher(DefaultClient)
	DefaultWatchdog = NewWatchdog(DefaultClient, DefaultRefresher)
	DefaultProxy = NewProxy(DefaultClient, DefaultRotator, DefaultRefresher)
}
