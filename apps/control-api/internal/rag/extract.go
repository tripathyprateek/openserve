package rag

import (
	"bytes"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/ledongthuc/pdf"
)

// ExtractText returns plain text from a file based on its extension.
func ExtractText(r io.ReadSeeker, filename string, size int64) (string, error) {
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(filename), "."))
	switch ext {
	case "txt", "md":
		data, err := io.ReadAll(r)
		if err != nil {
			return "", fmt.Errorf("extract: read: %w", err)
		}
		return string(data), nil
	case "pdf":
		return extractPDF(r, size)
	default:
		return "", fmt.Errorf("extract: unsupported file type %q", ext)
	}
}

func extractPDF(r io.ReadSeeker, size int64) (string, error) {
	// pdf.NewReader needs io.ReaderAt; buffer the file to get both interfaces.
	data, err := io.ReadAll(r)
	if err != nil {
		return "", fmt.Errorf("extract: read pdf: %w", err)
	}
	rdr, err := pdf.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", fmt.Errorf("extract: pdf reader: %w", err)
	}
	var sb strings.Builder
	for i := 1; i <= rdr.NumPage(); i++ {
		page := rdr.Page(i)
		if page.V.IsNull() {
			continue
		}
		text, err := page.GetPlainText(nil)
		if err != nil {
			continue // best effort
		}
		sb.WriteString(text)
		sb.WriteString("\n\n")
	}
	return sb.String(), nil
}
