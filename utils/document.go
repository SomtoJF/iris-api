package utils

import (
	"bytes"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/ledongthuc/pdf"
	"github.com/nguyenthenguyen/docx"
)

func ExtractTextFromPDF(file io.Reader) (string, error) {
	data, err := io.ReadAll(file)
	if err != nil {
		return "", err
	}
	r, err := pdf.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", err
	}
	plainReader, err := r.GetPlainText()
	if err != nil {
		return "", err
	}
	content, err := io.ReadAll(plainReader)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(content)), nil
}

func ExtractTextFromDOCX(file io.Reader, fileSize int64) (string, error) {
	data, err := io.ReadAll(file)
	if err != nil {
		return "", err
	}
	doc, err := docx.ReadDocxFromMemory(bytes.NewReader(data), fileSize)
	if err != nil {
		return "", err
	}
	defer doc.Close()

	text := doc.Editable().GetContent()
	return strings.TrimSpace(text), nil
}

func ExtractTextFromDocument(file io.Reader, filename string, fileSize int64) (string, error) {
	ext := strings.ToLower(filepath.Ext(filename))

	switch ext {
	case ".pdf":
		return ExtractTextFromPDF(file)
	case ".doc", ".docx":
		return ExtractTextFromDOCX(file, fileSize)
	default:
		return "", fmt.Errorf("unsupported file format: %s", ext)
	}
}
