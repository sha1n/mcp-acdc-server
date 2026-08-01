package domain

type SourceDocument struct {
	ID            string
	URI           string
	Name          string
	Description   string
	MIMEType      string
	FilePath      string
	RelativePath  string
	Content       string
	Fingerprint   string
	BodyStartLine int
	Keywords      []string
	PathLabels    []string
}

type Chunk struct {
	ID              string   `json:"chunk_id"`
	SourceID        string   `json:"source_id"`
	SourceURI       string   `json:"source_uri"`
	ChunkURI        string   `json:"chunk_uri"`
	SourceTitle     string   `json:"source_title"`
	SourcePath      string   `json:"source_path"`
	PathLabels      []string `json:"path_labels"`
	HeadingPath     []string `json:"heading_path"`
	SectionFragment string   `json:"section_fragment"`
	Fragment        string   `json:"fragment"`
	Part            int      `json:"part"`
	PartCount       int      `json:"part_count"`
	StartLine       int      `json:"start_line"`
	EndLine         int      `json:"end_line"`
	Content         string   `json:"content"`
	Keywords        []string `json:"keywords,omitempty"`
}
