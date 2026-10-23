package quota

type Limits struct {
	RunsPerMinute     int
	MessagesPerMinute int
	UploadsPerMinute  int
	SSEConnections    int
}

func DefaultLimits() Limits {
	return Limits{RunsPerMinute: 30, MessagesPerMinute: 60, UploadsPerMinute: 10, SSEConnections: 4}
}
