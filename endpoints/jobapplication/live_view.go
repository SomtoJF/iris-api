package jobapplication

import (
	"net/http"
	"net/url"

	"github.com/SomtoJF/iris-api/model"
	"github.com/SomtoJF/iris-api/utils"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

type browserLiveViewSession struct {
	ApplicationBrowserID uuid.UUID `gorm:"column:application_browser_id"`
	Provider             string    `gorm:"column:provider"`
	Status               string    `gorm:"column:status"`
	URLCiphertext        []byte    `gorm:"column:browser_live_view_url_ciphertext"`
}

func (e *Endpoint) GetApplicationLiveViewURL(c *gin.Context) {
	userID := c.GetUint("userId")
	if userID == 0 {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}
	externalID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid job application ID"})
		return
	}

	var application model.JobApplication
	if err := e.db.Select("id_job_application").
		Where("id_external = ? AND id_user = ? AND deleted_at IS NULL", externalID, userID).
		First(&application).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			c.JSON(http.StatusNotFound, gin.H{"error": "Job application not found"})
			return
		}
		e.logger.ErrorContext(c.Request.Context(), "failed to authorize application live view", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to load application"})
		return
	}

	var session browserLiveViewSession
	if err := e.db.Table("browser_session").
		Select("application_browser_id, provider, status, browser_live_view_url_ciphertext").
		Where("id_job_application = ? AND provider = ? AND status = ?", application.IdJobApplication, "kernel", "active").
		First(&session).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			c.JSON(http.StatusNotFound, gin.H{"error": "No active browser view"})
			return
		}
		e.logger.ErrorContext(c.Request.Context(), "failed to load application live view", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to load browser view"})
		return
	}
	if len(session.URLCiphertext) == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "No active browser view"})
		return
	}
	liveViewURL, err := utils.DecryptBrowserSecret(session.URLCiphertext, session.ApplicationBrowserID)
	if err != nil {
		e.logger.ErrorContext(c.Request.Context(), "failed to decrypt application live view URL", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to load browser view"})
		return
	}
	parsedURL, err := url.Parse(liveViewURL)
	if err != nil || parsedURL.Scheme != "https" || parsedURL.Host == "" || parsedURL.User != nil {
		e.logger.ErrorContext(c.Request.Context(), "stored application live view URL is invalid")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to load browser view"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": gin.H{"url": liveViewURL}})
}
