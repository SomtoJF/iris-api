package utils

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	md "github.com/JohannesKaufmann/html-to-markdown"
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
	text := strings.Join(strings.FieldsFunc(strings.TrimSpace(string(content)), func(r rune) bool { return r == '\n' || r == '\r' }), " ")

	return text, nil
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

	// Convert to markdown
	converter := md.NewConverter("", true, nil)
	markdown, err := converter.ConvertString(text)
	if err != nil {
		return "", fmt.Errorf("failed to convert html to markdown: %w", err)
	}

	// Clean up markdown
	markdown = strings.TrimSpace(markdown)
	fmt.Println(markdown)
	return "", errors.New("test")
	return markdown, nil
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
