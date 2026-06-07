package tools

const (
	defaultForegroundShellWaitMS = 15_000
	maxForegroundShellWaitMS     = 120_000

	defaultShellWaitTimeoutMS = 20_000
	maxShellWaitTimeoutMS     = 120_000

	defaultBackgroundShellTimeoutMS = 1_800_000
	maxBackgroundShellTimeoutMS     = 1_800_000

	maxShellStdinBytes       = 512
	shellStdinWriteTimeoutMS = 2_000
	shellStdinReadyTimeoutMS = 2_000
)
