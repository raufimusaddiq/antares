package blog

// Limits are the numbers the author is held to, stated before they publish and not after a failure (spec: edge cases).
// Every value is read from the constant that is enforced, so what the author is told cannot drift from what happens.
type Limits struct {
	CheckBytes      int `json:"checkBytes"`      // text the assistant reads in one check
	DocBytes        int `json:"docBytes"`        // a saved post
	ImagesPerPost   int `json:"imagesPerPost"`   // uploaded images in one post
	ImageBytes      int `json:"imageBytes"`      // one uploaded image
	ImageBytesTotal int `json:"imageBytesTotal"` // everything one author keeps as post images, across all posts
	BlocksPerCheck  int `json:"blocksPerCheck"`  // blocks the assistant may propose in one check
	SourcesPerBlock int `json:"sourcesPerBlock"` // sources one block may name
	ChecksPer24h    int `json:"checksPer24h"`    // checks one author may start in a rolling 24 hours
}

// CurrentLimits is what the Limits route answers.
func CurrentLimits() Limits {
	return Limits{
		CheckBytes:      maxCheckInput,
		DocBytes:        maxDocBytes,
		ImagesPerPost:   maxAssetsPerPost,
		ImageBytes:      maxAssetBytes,
		ImageBytesTotal: maxAssetBytesPerAuthor,
		BlocksPerCheck:  maxBlocks,
		SourcesPerBlock: maxSourcesPer,
		ChecksPer24h:    maxChecksPer24h,
	}
}
