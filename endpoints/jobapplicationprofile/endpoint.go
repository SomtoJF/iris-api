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

type UpsertJobApplicationProfileRequest struct {
	FirstName              string   `json:"firstName" binding:"required"`
	LastName               string   `json:"lastName" binding:"required"`
	Email                  string   `json:"email" binding:"required,email"`
	Phone                  string   `json:"phone" binding:"required"`
	Address                string   `json:"address" binding:"required"`
	City                   string   `json:"city" binding:"required"`
	State                  string   `json:"state" binding:"required"`
	Zip                    string   `json:"zip" binding:"required"`
	CountryOfResidence     string   `json:"countryOfResidence" binding:"required"`
	IsVeteran              bool     `json:"isVeteran"`
	CountriesOfCitizenship []string `json:"countriesOfCitizenship" binding:"required"`
	Gender                 string   `json:"gender" binding:"required"`
	// Date of birth in ISO 8601 format
	DateOfBirth string `json:"dateOfBirth" binding:"required"`
}

type GetJobApplicationProfileResponse struct {
	Id                     string   `json:"id"`
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
		Id:                     jobApplicationProfile.IdExternal.String(),
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

// post /jobapplicationprofile
func (e *Endpoint) UpsertJobApplicationProfile(c *gin.Context) {
	userId := c.GetUint("userId")
	if userId == 0 {
		e.logger.Printf("Unauthorized user: %d", userId)
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	var input UpsertJobApplicationProfileRequest
	if err := c.ShouldBindJSON(&input); err != nil {
		e.logger.Printf("Failed to bind JSON: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "Failed to bind JSON"})
		return
	}

	dateOfBirth, err := time.Parse(time.RFC3339, input.DateOfBirth)
	if err != nil {
		dateOfBirth, err = time.Parse("2006-01-02", input.DateOfBirth)
		if err != nil {
			e.logger.Printf("Invalid date of birth format: %v", err)
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid dateOfBirth; use ISO 8601 format (e.g. 2006-01-02)"})
			return
		}
	}

	var jobApplicationProfile model.JobApplicationProfile
	err = e.db.Where("id_user = ?", userId).First(&jobApplicationProfile).Error
	if err != nil && err != gorm.ErrRecordNotFound {
		e.logger.Printf("Failed to find job application profile: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to find job application profile"})
		return
	}

	if err == gorm.ErrRecordNotFound {
		jobApplicationProfile = model.JobApplicationProfile{
			UserId:                 userId,
			FirstName:              input.FirstName,
			LastName:               input.LastName,
			Email:                  input.Email,
			Phone:                  input.Phone,
			Address:                input.Address,
			City:                   input.City,
			State:                  input.State,
			Zip:                    input.Zip,
			CountryOfResidence:     input.CountryOfResidence,
			IsVeteran:              input.IsVeteran,
			CountriesOfCitizenship: input.CountriesOfCitizenship,
			Gender:                 input.Gender,
			DateOfBirth:            dateOfBirth,
		}
		if err := e.db.Create(&jobApplicationProfile).Error; err != nil {
			e.logger.Printf("Failed to create job application profile: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create job application profile"})
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"message": "Job application profile created successfully",
			"data":    jobApplicationProfile,
		})
		return
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
