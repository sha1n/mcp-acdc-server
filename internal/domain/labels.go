package domain

import "strings"

// Labels returns the deduplicated, normalised union of pathLabels then keywords.
func Labels(pathLabels, keywords []string) []string {
	labels := make([]string, 0, len(pathLabels)+len(keywords))
	seen := make(map[string]struct{}, len(pathLabels)+len(keywords))
	for _, group := range [][]string{pathLabels, keywords} {
		for _, raw := range group {
			label := strings.ToLower(strings.TrimSpace(raw))
			if label == "" {
				continue
			}
			if _, duplicate := seen[label]; duplicate {
				continue
			}
			seen[label] = struct{}{}
			labels = append(labels, label)
		}
	}
	return labels
}

// Labels returns the chunk's normalised path labels and keywords.
func (c Chunk) Labels() []string {
	return Labels(c.PathLabels, c.Keywords)
}

// Labels returns the source document's normalised path labels and keywords.
func (s SourceDocument) Labels() []string {
	return Labels(s.PathLabels, s.Keywords)
}
