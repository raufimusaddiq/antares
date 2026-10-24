package quota

type Limits struct {
	RunsPerMinute      int
	MessagesPerMinute  int
	UploadsPerMinute   int
	ArtifactsPerMinute int
	HistoryPerMinute   int
	// DraftWritesPerMinute covers composer-draft and personal-template writes (PUT/DELETE draft, POST/PUT/DELETE template).
	DraftWritesPerMinute int
	SSEConnections       int
}

func DefaultLimits() Limits {
	return Limits{RunsPerMinute: 30, MessagesPerMinute: 60, UploadsPerMinute: 10, ArtifactsPerMinute: 60, HistoryPerMinute: 240, DraftWritesPerMinute: 240, SSEConnections: 4}
}
