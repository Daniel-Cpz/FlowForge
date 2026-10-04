package job

// Delivery is transport metadata, never authoritative job data.
type Delivery struct {
	MessageID string
	JobID     string
	Version   string
	Malformed bool
}
