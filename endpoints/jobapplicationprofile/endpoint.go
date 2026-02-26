package jobapplicationprofile

import (
	"log"
	"net/http"
	"time"

	"github.com/SomtoJF/iris-api/model"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type Endpoint struct {
	db     *gorm.DB
	logger *log.Logger
}

func NewEndpoint(db *gorm.DB, logger *log.Logger) *Endpoint {
	return &Endpoint{db: db, logger: logger}
}

type UpdateJobApplicationProfileRequest struct {
	FirstName              string   `json:"firstName" binding:"required"`
	LastName               string   `json:"lastName" binding:"required"`
	Email                  string   `json:"email" binding:"required,email"`
	Phone                  string   `json:"phone" binding:"required"`
	Address                string   `json:"address" binding:"required"`
	City                   string   `json:"city" binding:"required"`
	State                  string   `json:"state" binding:"required"`
	Zip                    string   `json:"zip" binding:"required"`
	CountryOfResidence     string   `json:"countryOfResidence" binding:"required"`
	IsVeteran              bool     `json:"isVeteran" binding:"required"`
	CountriesOfCitizenship []string `json:"countriesOfCitizenship" binding:"required"`
	Gender                 string   `json:"gender" binding:"required"`
	// Date of birth in ISO 8601 format
	DateOfBirth string `json:"dateOfBirth" binding:"required"`
}

type GetJobApplicationProfileResponse struct {
	FirstName              string   `json:"firstName"`
	LastName               string   `json:"lastName"`
	Email                  string   `json:"email"`
	Phone                  string   `json:"phone"`
	Address                string   `json:"address"`
	City                   string   `json:"city"`
	State                  string   `json:"state"`
	Zip                    string   `json:"zip"`
	CountryOfResidence     string   `json:"countryOfResidence"`
	IsVeteran              bool     `json:"isVeteran"`
	CountriesOfCitizenship []string `json:"countriesOfCitizenship"`
	Gender                 string   `json:"gender"`
	DateOfBirth            string   `json:"dateOfBirth"`
}

// get /jobapplicationprofile
func (e *Endpoint) GetJobApplicationProfile(c *gin.Context) {
	userId := c.GetUint("userId")
	if userId == 0 {
		e.logger.Printf("Unauthorized user: %d", userId)
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	var jobApplicationProfile model.JobApplicationProfile
	if err := e.db.Where("id_user = ?", userId).First(&jobApplicationProfile).Error; err != nil {
		e.logger.Printf("Failed to find job application profile: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to find job application profile"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"data": GetJobApplicationProfileResponse{
		FirstName:              jobApplicationProfile.FirstName,
		LastName:               jobApplicationProfile.LastName,
		Email:                  jobApplicationProfile.Email,
		Phone:                  jobApplicationProfile.Phone,
		Address:                jobApplicationProfile.Address,
		City:                   jobApplicationProfile.City,
		State:                  jobApplicationProfile.State,
		Zip:                    jobApplicationProfile.Zip,
		CountryOfResidence:     jobApplicationProfile.CountryOfResidence,
		IsVeteran:              jobApplicationProfile.IsVeteran,
		CountriesOfCitizenship: jobApplicationProfile.CountriesOfCitizenship,
		Gender:                 jobApplicationProfile.Gender,
		DateOfBirth:            jobApplicationProfile.DateOfBirth.Format(time.RFC3339),
	}})
}

// put /jobapplicationprofile
func (e *Endpoint) UpdateJobApplicationProfile(c *gin.Context) {
	var input UpdateJobApplicationProfileRequest
	if err := c.ShouldBindJSON(&input); err != nil {
		e.logger.Printf("Failed to bind JSON: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "Failed to bind JSON"})
		return
	}

	userId := c.GetUint("userId")
	if userId == 0 {
		e.logger.Printf("Unauthorized user: %d", userId)
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	var jobApplicationProfile model.JobApplicationProfile
	if err := e.db.Where("id_user = ?", userId).First(&jobApplicationProfile).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			e.logger.Printf("Job application profile not found for user %d", userId)
			c.JSON(http.StatusNotFound, gin.H{"error": "Job application profile not found"})
			return
		}
		e.logger.Printf("Failed to find job application profile: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to find job application profile"})
		return
	}

	dateOfBirth, err := time.Parse(time.RFC3339, input.DateOfBirth)
	if err != nil {
		// Try date-only ISO 8601
		dateOfBirth, err = time.Parse("2006-01-02", input.DateOfBirth)
		if err != nil {
			e.logger.Printf("Invalid date of birth format: %v", err)
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid dateOfBirth; use ISO 8601 format (e.g. 2006-01-02)"})
			return
		}
	}

	jobApplicationProfile.FirstName = input.FirstName
	jobApplicationProfile.LastName = input.LastName
	jobApplicationProfile.Email = input.Email
	jobApplicationProfile.Phone = input.Phone
	jobApplicationProfile.Address = input.Address
	jobApplicationProfile.City = input.City
	jobApplicationProfile.State = input.State
	jobApplicationProfile.Zip = input.Zip
	jobApplicationProfile.CountryOfResidence = input.CountryOfResidence
	jobApplicationProfile.IsVeteran = input.IsVeteran
	jobApplicationProfile.CountriesOfCitizenship = input.CountriesOfCitizenship
	jobApplicationProfile.Gender = input.Gender
	jobApplicationProfile.DateOfBirth = dateOfBirth

	if err := e.db.Save(&jobApplicationProfile).Error; err != nil {
		e.logger.Printf("Failed to update job application profile: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update job application profile"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "Job application profile updated successfully",
		"data":    jobApplicationProfile,
	})
}
