package jobapplicationprofile

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/SomtoJF/iris-api/model"
	"github.com/gin-gonic/gin"
	"github.com/lib/pq"
	"gorm.io/gorm"
)

type Endpoint struct {
	db     *gorm.DB
	logger *slog.Logger
}

func NewEndpoint(db *gorm.DB, logger *slog.Logger) *Endpoint {
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
	DateOfBirth                 string                      `json:"dateOfBirth" binding:"required"`
	SalaryMin                   *float64                    `json:"salaryMin"`
	SalaryMax                   *float64                    `json:"salaryMax"`
	SalaryCurrency              string                      `json:"salaryCurrency"`
	Ethnicity                   string                      `json:"ethnicity"`
	IsOpenToRelocating          *bool                       `json:"isOpenToRelocating"`
	NoticePeriodDays            *int                        `json:"noticePeriodDays"`
	LinkedInUrl                 string                      `json:"linkedinUrl" binding:"required"`
	PreferredWorkingArrangement []string                    `json:"preferredWorkingArrangement"`
	LanguageProficiencies       model.LanguageProficiencies `json:"languageProficiencies"`
	PortfolioLink               *string                     `json:"portfolioLink"`
}

type GetJobApplicationProfileResponse struct {
	Id                          string                      `json:"id"`
	FirstName                   string                      `json:"firstName"`
	LastName                    string                      `json:"lastName"`
	Email                       string                      `json:"email"`
	Phone                       string                      `json:"phone"`
	Address                     string                      `json:"address"`
	City                        string                      `json:"city"`
	State                       string                      `json:"state"`
	Zip                         string                      `json:"zip"`
	CountryOfResidence          string                      `json:"countryOfResidence"`
	IsVeteran                   bool                        `json:"isVeteran"`
	CountriesOfCitizenship      []string                    `json:"countriesOfCitizenship"`
	Gender                      string                      `json:"gender"`
	DateOfBirth                 string                      `json:"dateOfBirth"`
	SalaryMin                   *float64                    `json:"salaryMin"`
	SalaryMax                   *float64                    `json:"salaryMax"`
	SalaryCurrency              string                      `json:"salaryCurrency"`
	Ethnicity                   string                      `json:"ethnicity"`
	IsOpenToRelocating          *bool                       `json:"isOpenToRelocating"`
	NoticePeriodDays            *int                        `json:"noticePeriodDays"`
	LinkedInUrl                 *string                     `json:"linkedinUrl"`
	PreferredWorkingArrangement []string                    `json:"preferredWorkingArrangement"`
	LanguageProficiencies       model.LanguageProficiencies `json:"languageProficiencies"`
	PortfolioLink               *string                     `json:"portfolioLink"`
}

// get /jobapplicationprofile
func (e *Endpoint) GetJobApplicationProfile(c *gin.Context) {
	userId := c.GetUint("userId")
	if userId == 0 {
		e.logger.InfoContext(c.Request.Context(), "unauthorized", "handler", "GetJobApplicationProfile")
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	var jobApplicationProfile model.JobApplicationProfile
	if err := e.db.Where("id_user = ?", userId).First(&jobApplicationProfile).Error; err != nil {
		e.logger.ErrorContext(c.Request.Context(), "failed to find job application profile", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to find job application profile"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"data": GetJobApplicationProfileResponse{
		Id:                          jobApplicationProfile.IdExternal.String(),
		FirstName:                   jobApplicationProfile.FirstName,
		LastName:                    jobApplicationProfile.LastName,
		Email:                       jobApplicationProfile.Email,
		Phone:                       jobApplicationProfile.Phone,
		Address:                     jobApplicationProfile.Address,
		City:                        jobApplicationProfile.City,
		State:                       jobApplicationProfile.State,
		Zip:                         jobApplicationProfile.Zip,
		CountryOfResidence:          jobApplicationProfile.CountryOfResidence,
		IsVeteran:                   jobApplicationProfile.IsVeteran,
		CountriesOfCitizenship:      []string(jobApplicationProfile.CountriesOfCitizenship),
		Gender:                      jobApplicationProfile.Gender,
		DateOfBirth:                 jobApplicationProfile.DateOfBirth.Format(time.RFC3339),
		SalaryMin:                   jobApplicationProfile.SalaryMin,
		SalaryMax:                   jobApplicationProfile.SalaryMax,
		SalaryCurrency:              jobApplicationProfile.SalaryCurrency,
		Ethnicity:                   jobApplicationProfile.Ethnicity,
		IsOpenToRelocating:          jobApplicationProfile.IsOpenToRelocating,
		NoticePeriodDays:            jobApplicationProfile.NoticePeriodDays,
		LinkedInUrl:                 jobApplicationProfile.LinkedInUrl,
		PreferredWorkingArrangement: []string(jobApplicationProfile.PreferredWorkingArrangement),
		LanguageProficiencies:       jobApplicationProfile.LanguageProficiencies,
		PortfolioLink:               jobApplicationProfile.PortfolioLink,
	}})
}

// post /jobapplicationprofile
func (e *Endpoint) UpsertJobApplicationProfile(c *gin.Context) {
	userId := c.GetUint("userId")
	if userId == 0 {
		e.logger.InfoContext(c.Request.Context(), "unauthorized", "handler", "UpsertJobApplicationProfile")
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	var input UpsertJobApplicationProfileRequest
	if err := c.ShouldBindJSON(&input); err != nil {
		e.logger.WarnContext(c.Request.Context(), "failed to bind JSON", "handler", "UpsertJobApplicationProfile", "error", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "Failed to bind JSON"})
		return
	}

	dateOfBirth, err := time.Parse(time.RFC3339, input.DateOfBirth)
	if err != nil {
		dateOfBirth, err = time.Parse("2006-01-02", input.DateOfBirth)
		if err != nil {
			e.logger.WarnContext(c.Request.Context(), "invalid date of birth format", "error", err)
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid dateOfBirth; use ISO 8601 format (e.g. 2006-01-02)"})
			return
		}
	}

	var jobApplicationProfile model.JobApplicationProfile
	err = e.db.Where("id_user = ?", userId).First(&jobApplicationProfile).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		e.logger.ErrorContext(c.Request.Context(), "failed to find job application profile", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to find job application profile"})
		return
	}

	cleanedLinkedIn := cleanupLinkedInUrl(input.LinkedInUrl)

	if errors.Is(err, gorm.ErrRecordNotFound) {
		jobApplicationProfile = model.JobApplicationProfile{
			UserId:                      userId,
			FirstName:                   input.FirstName,
			LastName:                    input.LastName,
			Email:                       input.Email,
			Phone:                       input.Phone,
			Address:                     input.Address,
			City:                        input.City,
			State:                       input.State,
			Zip:                         input.Zip,
			CountryOfResidence:          input.CountryOfResidence,
			IsVeteran:                   input.IsVeteran,
			CountriesOfCitizenship:      pq.StringArray(input.CountriesOfCitizenship),
			Gender:                      input.Gender,
			DateOfBirth:                 dateOfBirth,
			SalaryMin:                   input.SalaryMin,
			SalaryMax:                   input.SalaryMax,
			SalaryCurrency:              input.SalaryCurrency,
			Ethnicity:                   input.Ethnicity,
			IsOpenToRelocating:          input.IsOpenToRelocating,
			NoticePeriodDays:            input.NoticePeriodDays,
			PreferredWorkingArrangement: pq.StringArray(input.PreferredWorkingArrangement),
			LinkedInUrl:                 &cleanedLinkedIn,
			LanguageProficiencies:       input.LanguageProficiencies,
			PortfolioLink:               input.PortfolioLink,
		}
		if err := e.db.Create(&jobApplicationProfile).Error; err != nil {
			e.logger.ErrorContext(c.Request.Context(), "failed to create job application profile", "error", err)
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
	jobApplicationProfile.CountriesOfCitizenship = pq.StringArray(input.CountriesOfCitizenship)
	jobApplicationProfile.Gender = input.Gender
	jobApplicationProfile.DateOfBirth = dateOfBirth
	jobApplicationProfile.SalaryMin = input.SalaryMin
	jobApplicationProfile.SalaryMax = input.SalaryMax
	jobApplicationProfile.SalaryCurrency = input.SalaryCurrency
	jobApplicationProfile.Ethnicity = input.Ethnicity
	jobApplicationProfile.IsOpenToRelocating = input.IsOpenToRelocating
	jobApplicationProfile.NoticePeriodDays = input.NoticePeriodDays
	jobApplicationProfile.PreferredWorkingArrangement = pq.StringArray(input.PreferredWorkingArrangement)
	jobApplicationProfile.LanguageProficiencies = input.LanguageProficiencies
	jobApplicationProfile.PortfolioLink = input.PortfolioLink
	jobApplicationProfile.LinkedInUrl = &cleanedLinkedIn

	if err := e.db.Save(&jobApplicationProfile).Error; err != nil {
		e.logger.ErrorContext(c.Request.Context(), "failed to update job application profile", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update job application profile"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "Job application profile updated successfully",
		"data":    jobApplicationProfile,
	})
}

type PatchJobApplicationProfileRequest struct {
	FirstName                   *string                      `json:"firstName"`
	LastName                    *string                      `json:"lastName"`
	Email                       *string                      `json:"email"`
	Phone                       *string                      `json:"phone"`
	Address                     *string                      `json:"address"`
	City                        *string                      `json:"city"`
	State                       *string                      `json:"state"`
	Zip                         *string                      `json:"zip"`
	CountryOfResidence          *string                      `json:"countryOfResidence"`
	IsVeteran                   *bool                        `json:"isVeteran"`
	CountriesOfCitizenship      *[]string                    `json:"countriesOfCitizenship"`
	Gender                      *string                      `json:"gender"`
	DateOfBirth                 *string                      `json:"dateOfBirth"`
	SalaryMin                   *float64                     `json:"salaryMin"`
	SalaryMax                   *float64                     `json:"salaryMax"`
	SalaryCurrency              *string                      `json:"salaryCurrency"`
	Ethnicity                   *string                      `json:"ethnicity"`
	IsOpenToRelocating          *bool                        `json:"isOpenToRelocating"`
	NoticePeriodDays            *int                         `json:"noticePeriodDays"`
	LinkedInUrl                 *string                      `json:"linkedinUrl"`
	PreferredWorkingArrangement *[]string                    `json:"preferredWorkingArrangement"`
	LanguageProficiencies       *model.LanguageProficiencies `json:"languageProficiencies"`
	PortfolioLink               *string                      `json:"portfolioLink"`
}

// patch /jobapplicationprofile
func (e *Endpoint) PatchJobApplicationProfile(c *gin.Context) {
	userId := c.GetUint("userId")
	if userId == 0 {
		e.logger.InfoContext(c.Request.Context(), "unauthorized", "handler", "PatchJobApplicationProfile")
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	var input PatchJobApplicationProfileRequest
	if err := c.ShouldBindJSON(&input); err != nil {
		e.logger.WarnContext(c.Request.Context(), "failed to bind JSON", "handler", "PatchJobApplicationProfile", "error", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request body"})
		return
	}

	var profile model.JobApplicationProfile
	if err := e.db.Where("id_user = ?", userId).First(&profile).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Job application profile not found"})
		return
	}

	updates := buildProfileUpdateMap(input)
	if len(updates) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "No fields to update"})
		return
	}

	if input.DateOfBirth != nil {
		parsed, err := parseDateOfBirth(*input.DateOfBirth)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid dateOfBirth; use ISO 8601 format (e.g. 2006-01-02)"})
			return
		}
		updates["date_of_birth"] = parsed
	}

	if err := e.db.Model(&profile).Updates(updates).Error; err != nil {
		e.logger.ErrorContext(c.Request.Context(), "failed to patch job application profile", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update job application profile"})
		return
	}

	// Reload for response
	e.db.Where("id_user = ?", userId).First(&profile)

	c.JSON(http.StatusOK, gin.H{
		"message": "Job application profile updated successfully",
		"data": GetJobApplicationProfileResponse{
			Id:                          profile.IdExternal.String(),
			FirstName:                   profile.FirstName,
			LastName:                    profile.LastName,
			Email:                       profile.Email,
			Phone:                       profile.Phone,
			Address:                     profile.Address,
			City:                        profile.City,
			State:                       profile.State,
			Zip:                         profile.Zip,
			CountryOfResidence:          profile.CountryOfResidence,
			IsVeteran:                   profile.IsVeteran,
			CountriesOfCitizenship:      []string(profile.CountriesOfCitizenship),
			Gender:                      profile.Gender,
			DateOfBirth:                 profile.DateOfBirth.Format(time.RFC3339),
			SalaryMin:                   profile.SalaryMin,
			SalaryMax:                   profile.SalaryMax,
			SalaryCurrency:              profile.SalaryCurrency,
			Ethnicity:                   profile.Ethnicity,
			IsOpenToRelocating:          profile.IsOpenToRelocating,
			NoticePeriodDays:            profile.NoticePeriodDays,
			LinkedInUrl:                 profile.LinkedInUrl,
			PreferredWorkingArrangement: []string(profile.PreferredWorkingArrangement),
			LanguageProficiencies:       profile.LanguageProficiencies,
			PortfolioLink:               profile.PortfolioLink,
		},
	})
}

func buildProfileUpdateMap(input PatchJobApplicationProfileRequest) map[string]interface{} {
	updates := map[string]interface{}{}
	if input.FirstName != nil {
		updates["first_name"] = *input.FirstName
	}
	if input.LastName != nil {
		updates["last_name"] = *input.LastName
	}
	if input.Email != nil {
		updates["email"] = *input.Email
	}
	if input.Phone != nil {
		updates["phone"] = *input.Phone
	}
	if input.Address != nil {
		updates["address"] = *input.Address
	}
	if input.City != nil {
		updates["city"] = *input.City
	}
	if input.State != nil {
		updates["state"] = *input.State
	}
	if input.Zip != nil {
		updates["zip"] = *input.Zip
	}
	if input.CountryOfResidence != nil {
		updates["country_of_residence"] = *input.CountryOfResidence
	}
	if input.IsVeteran != nil {
		updates["is_veteran"] = *input.IsVeteran
	}
	if input.CountriesOfCitizenship != nil {
		updates["countries_of_citizenship"] = pq.StringArray(*input.CountriesOfCitizenship)
	}
	if input.Gender != nil {
		updates["gender"] = *input.Gender
	}
	if input.SalaryMin != nil {
		updates["salary_min"] = *input.SalaryMin
	}
	if input.SalaryMax != nil {
		updates["salary_max"] = *input.SalaryMax
	}
	if input.SalaryCurrency != nil {
		updates["salary_currency"] = *input.SalaryCurrency
	}
	if input.Ethnicity != nil {
		updates["ethnicity"] = *input.Ethnicity
	}
	if input.IsOpenToRelocating != nil {
		updates["is_open_to_relocating"] = *input.IsOpenToRelocating
	}
	if input.NoticePeriodDays != nil {
		updates["notice_period_days"] = *input.NoticePeriodDays
	}
	if input.LinkedInUrl != nil {
		updates["linkedin_url"] = cleanupLinkedInUrl(*input.LinkedInUrl)
	}
	if input.PreferredWorkingArrangement != nil {
		updates["preferred_working_arrangement"] = pq.StringArray(*input.PreferredWorkingArrangement)
	}
	if input.LanguageProficiencies != nil {
		updates["language_proficiencies"] = *input.LanguageProficiencies
	}
	if input.PortfolioLink != nil {
		updates["portfolio_link"] = *input.PortfolioLink
	}
	return updates
}

func cleanupLinkedInUrl(url string) string {
	url = strings.TrimRight(url, "/")
	url = strings.Replace(url, "https://www.", "https://", 1)
	return url
}

func parseDateOfBirth(s string) (time.Time, error) {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t, err = time.Parse("2006-01-02", s)
	}
	return t, err
}
