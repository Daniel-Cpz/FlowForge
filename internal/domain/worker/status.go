package worker

type Status string

const (
	Online   Status = "ONLINE"
	Idle     Status = "IDLE"
	Busy     Status = "BUSY"
	Draining Status = "DRAINING"
	Offline  Status = "OFFLINE"
)
