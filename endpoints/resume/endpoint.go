package resume

import (
	"bytes"
	"context"
	"io"
	"log"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/SomtoJF/iris-api/model"
	s3pkg "github.com/SomtoJF/iris-api/pkg/s3"
	"github.com/SomtoJF/iris-api/utils"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

type Endpoint struct {
	db        *gorm.DB
	s3Manager *s3pkg.S3Manager
	logger    *log.Logger
}

func NewEndpoint(db *gorm.DB, s3Manager *s3pkg.S3Manager, logger *log.Logger) *Endpoint {
	return &Endpoint{db: db, s3Manager: s3Manager, logger: logger}
}

type ResumeDTO struct {
	Id        string    `json:"id"`
	FileName  string    `json:"fileName"`
	FileSize  int64     `json:"fileSize"`
	FileKey   string    `json:"fileKey"`
	IsActive  bool      `json:"isActive"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

func (e *Endpoint) FetchResumes(c *gin.Context) {
	userId := c.GetUint("userId")
	if userId == 0 {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	var resumes []model.Resume
	if err := e.db.Where("deleted_at IS NULL AND id_user = ?", userId).Order("created_at DESC").Find(&resumes).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch resumes"})
		return
	}

	resumeDTOs := make([]ResumeDTO, 0, len(resumes))
	for _, resume := range resumes {
		resumeDTOs = append(resumeDTOs, ResumeDTO{
			Id:        resume.IdExternal.String(),
			FileName:  resume.FileName,
			FileSize:  resume.FileSize,
			FileKey:   resume.FileKey,
			IsActive:  resume.IsActive,
			CreatedAt: resume.CreatedAt,
			UpdatedAt: resume.UpdatedAt,
		})
	}

	c.JSON(http.StatusOK, gin.H{"data": resumeDTOs})
}

func (e *Endpoint) SetResumeAsActive(c *gin.Context) {
	id := c.Param("id")
	userId := c.GetUint("userId")
	if userId == 0 {
		e.logger.Printf("Unauthorized user: %d", userId)
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	// Start transaction
	tx := e.db.Begin()
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
		}
	}()

	// Deactivate all resumes for the user
	if err := tx.Model(&model.Resume{}).Where("deleted_at IS NULL AND id_user = ?", userId).Update("is_active", false).Error; err != nil {
		tx.Rollback()
		e.logger.Printf("Failed to deactivate resumes: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to deactivate resumes"})
		return
	}

	// Find resume by IdExternal
	var resume model.Resume
	if err := tx.Where("id_external = ? AND deleted_at IS NULL AND id_user = ?", id, userId).First(&resume).Error; err != nil {
		tx.Rollback()
		if err == gorm.ErrRecordNotFound {
			e.logger.Printf("Resume not found: %v", err)
			c.JSON(http.StatusNotFound, gin.H{"error": "Resume not found"})
			return
		}
		e.logger.Printf("Failed to find resume: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to find resume"})
		return
	}

	// Set resume as active
	if err := tx.Model(&resume).Update("is_active", true).Error; err != nil {
		tx.Rollback()
		e.logger.Printf("Failed to activate resume: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to activate resume"})
		return
	}

	// Commit transaction
	if err := tx.Commit().Error; err != nil {
		e.logger.Printf("Failed to commit transaction: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to commit transaction"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Resume set as active"})
}

func (e *Endpoint) UploadResume(c *gin.Context) {
	userId := c.GetUint("userId")
	if userId == 0 {
		e.logger.Printf("Unauthorized user: %d", userId)
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	file, header, err := c.Request.FormFile("file")
	if err != nil {
		e.logger.Printf("Failed to get file from request: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "Failed to get file from request"})
		return
	}
	defer file.Close()

	// Validate file extension
	ext := strings.ToLower(filepath.Ext(header.Filename))
	if ext != ".pdf" && ext != ".doc" && ext != ".docx" {
		e.logger.Printf("Invalid file extension: %s", ext)
		c.JSON(http.StatusBadRequest, gin.H{"error": "Only PDF, DOC, and DOCX files are allowed"})
		return
	}

	// Validate file size (2MB max)
	const maxFileSize = 2 * 1024 * 1024
	if header.Size > maxFileSize {
		e.logger.Printf("File size exceeds 2MB: %d", header.Size)
		c.JSON(http.StatusBadRequest, gin.H{"error": "File size must not exceed 2MB"})
		return
	}

	// Read file content into buffer for reuse
	fileBytes, err := io.ReadAll(file)
	if err != nil {
		e.logger.Printf("Failed to read file: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to read file"})
		return
	}

	// Extract text from document
	content, err := utils.ExtractTextFromDocument(bytes.NewReader(fileBytes), header.Filename, header.Size)
	if err != nil {
		e.logger.Printf("Failed to extract text from document: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to extract text from document"})
		return
	}

	// Generate UUID and S3 key
	resumeUUID := uuid.New()
	s3Key := e.s3Manager.GenerateResumeKey(userId, header.Filename, resumeUUID.String())

	// Determine content type
	contentType := "application/pdf"
	if ext == ".doc" || ext == ".docx" {
		contentType = "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
	}

	// Upload to S3
	ctx := context.Background()
	if err := e.s3Manager.UploadFile(ctx, s3Key, bytes.NewReader(fileBytes), contentType); err != nil {
		e.logger.Printf("Failed to upload file to S3: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to upload file to S3"})
		return
	}

	// Create resume record
	resume := model.Resume{
		IdExternal:   resumeUUID,
		UserId:       userId,
		FileKey:      s3Key,
		FileName:     header.Filename,
		FileSize:     header.Size,
		Content:      content,
		IsProcessing: false,
		IsActive:     false,
	}

	if err := e.db.Create(&resume).Error; err != nil {
		// Attempt to delete from S3 on DB failure
		_ = e.s3Manager.DeleteFile(ctx, s3Key)
		e.logger.Printf("Failed to save resume: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save resume"})
		return
	}

	c.JSON(http.StatusCreated, gin.H{"data": ResumeDTO{
		Id:        resume.IdExternal.String(),
		FileName:  resume.FileName,
		FileSize:  resume.FileSize,
		FileKey:   resume.FileKey,
		IsActive:  resume.IsActive,
		CreatedAt: resume.CreatedAt,
		UpdatedAt: resume.UpdatedAt,
	}})
}

func (e *Endpoint) DeleteResume(c *gin.Context) {
	id := c.Param("id")
	userId := c.GetUint("userId")
	if userId == 0 {
		e.logger.Printf("Unauthorized user: %d", userId)
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	var resume model.Resume
	if err := e.db.Where("id_external = ? AND deleted_at IS NULL AND id_user = ?", id, userId).First(&resume).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			e.logger.Printf("Resume not found: %v", err)
			c.JSON(http.StatusNotFound, gin.H{"error": "Resume not found"})
			return
		}
		e.logger.Printf("Failed to find resume: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to find resume"})
		return
	}

	// Delete from S3
	ctx := context.Background()
	if err := e.s3Manager.DeleteFile(ctx, resume.FileKey); err != nil {
		e.logger.Printf("Failed to delete file from S3: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete file from S3"})
		return
	}

	// Soft delete in database
	now := time.Now()
	if err := e.db.Model(&resume).Update("deleted_at", now).Error; err != nil {
		e.logger.Printf("Failed to delete resume: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete resume"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Resume deleted successfully"})
}

func (e *Endpoint) GetResumeDownloadUrl(c *gin.Context) {
	id := c.Param("id")
	userId := c.GetUint("userId")
	if userId == 0 {
		e.logger.Printf("Unauthorized user: %d", userId)
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	var resume model.Resume
	if err := e.db.Where("id_external = ? AND deleted_at IS NULL AND id_user = ?", id, userId).First(&resume).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			e.logger.Printf("Resume not found: %v", err)
			c.JSON(http.StatusNotFound, gin.H{"error": "Resume not found"})
			return
		}
		e.logger.Printf("Failed to find resume: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to find resume"})
		return
	}

	// Generate presigned URL (15 minutes)
	ctx := context.Background()
	url, err := e.s3Manager.GeneratePresignedURL(ctx, resume.FileKey, 15*time.Minute)
	if err != nil {
		e.logger.Printf("Failed to generate download URL: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to generate download URL"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"url": url})
}
