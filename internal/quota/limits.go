package quota

type Limits struct {
	RunsPerMinute      int
	MessagesPerMinute  int
	UploadsPerMinute   int
	ArtifactsPerMinute int
	HistoryPerMinute   int
	SSEConnections     int
}

func DefaultLimits() Limits {
	return Limits{RunsPerMinute: 30, MessagesPerMinute: 60, UploadsPerMinute: 10, ArtifactsPerMinute: 60, HistoryPerMinute: 240, SSEConnections: 4}
}
