package rag

import (
	"strings"
	"unicode/utf8"
)

const (
	defaultChunkSize    = 512  // approx tokens (chars/4)
	defaultChunkOverlap = 64
)

// Chunk splits text into overlapping segments of roughly chunkSize characters.
func Chunk(text string, chunkSize, overlap int) []string {
	if chunkSize <= 0 {
		chunkSize = defaultChunkSize * 4 // chars
	}
	if overlap <= 0 {
		overlap = defaultChunkOverlap * 4
	}

	// Split on double newlines first (paragraph boundaries)
	paragraphs := strings.Split(text, "\n\n")
	var chunks []string
	var current strings.Builder

	for _, para := range paragraphs {
		para = strings.TrimSpace(para)
		if para == "" {
			continue
		}
		if current.Len()+utf8.RuneCountInString(para) > chunkSize && current.Len() > 0 {
			chunks = append(chunks, strings.TrimSpace(current.String()))
			// Overlap: keep tail of current chunk
			tail := current.String()
			if len(tail) > overlap {
				tail = tail[len(tail)-overlap:]
			}
			current.Reset()
			current.WriteString(tail)
			current.WriteString(" ")
		}
		current.WriteString(para)
		current.WriteString("\n\n")
	}
	if current.Len() > 0 {
		chunks = append(chunks, strings.TrimSpace(current.String()))
	}
	return chunks
}
