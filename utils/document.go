package utils

import (
	"fmt"
	"io"
	"path/filepath"
	"strings"
)

func ExtractTextFromPDF(file io.Reader) (string, error) {
	// TODO: Implement PDF text extraction
	return "", nil
}

func ExtractTextFromDOCX(file io.Reader) (string, error) {
	// TODO: Implement DOCX text extraction
	return "", nil
}

func ExtractTextFromDocument(file io.Reader, filename string) (string, error) {
	ext := strings.ToLower(filepath.Ext(filename))

	switch ext {
	case ".pdf":
		return ExtractTextFromPDF(file)
	case ".doc", ".docx":
		return ExtractTextFromDOCX(file)
	default:
		return "", fmt.Errorf("unsupported file format: %s", ext)
	}
}
