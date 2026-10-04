package job

type Status string

const (
	Queued     Status = "QUEUED"
	Running    Status = "RUNNING"
	Succeeded  Status = "SUCCEEDED"
	Failed     Status = "FAILED"
	Retrying   Status = "RETRYING"
	DeadLetter Status = "DEAD_LETTER"
	Cancelled  Status = "CANCELLED"
	TimedOut   Status = "TIMED_OUT"
)

func (s Status) Terminal() bool {
	return s == Succeeded || s == DeadLetter || s == Cancelled || s == TimedOut
}
