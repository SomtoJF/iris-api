package auth

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/SomtoJF/iris-api/model"
	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

type Endpoint struct {
	DB           *gorm.DB
	ClientDomain string
	logger       *slog.Logger
}

func NewEndpoint(db *gorm.DB, clientDomain string, logger *slog.Logger) *Endpoint {
	return &Endpoint{DB: db, ClientDomain: clientDomain, logger: logger}
}

type signUpInput struct {
	FirstName string `json:"firstName" binding:"required,max=50"`
	LastName  string `json:"lastName" binding:"required,max=50"`
	Email     string `json:"email" binding:"required,email"`
	Password  string `json:"password" binding:"required,max=20,min=8"`
}

type loginInput struct {
	Email    string `json:"email" binding:"required,email"`
	Password string `json:"password" binding:"required,max=20,min=8"`
}

type passwordResetRequest struct {
	Password    string `json:"password" binding:"required,max=20"`
	NewPassword string `json:"newPassword" binding:"required,max=20"`
}

// Login godoc
//
//	@Summary		Login user
//	@Description	Logs in a user and returns an access token
//	@Tags			auth
//	@Accept			json
//	@Produce		json
//	@Param			loginInput	body		loginInput				true	"Login credentials"
//	@Success		200			{object}	map[string]interface{}	"success message"
//	@Failure		400			{object}	map[string]interface{}	"error message"
//	@Failure		500			{object}	map[string]interface{}	"internal server error"
//	@Router			/login [post]
func (e *Endpoint) Login(c *gin.Context) {
	var body loginInput

	if err := c.ShouldBindJSON(&body); err != nil {
		e.logger.WarnContext(c.Request.Context(), "failed to bind JSON", "handler", "Login", "error", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	var userFound model.User
	if err := e.DB.Where("email = ?", body.Email).First(&userFound).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			e.logger.InfoContext(c.Request.Context(), "login failed", "reason", "invalid_credentials")
			c.JSON(http.StatusBadRequest, gin.H{"error": "email or password is incorrect"})
			return
		}
		e.logger.ErrorContext(c.Request.Context(), "failed to load user for login", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "An error occured"})
		return
	}

	if err := bcrypt.CompareHashAndPassword([]byte(userFound.PasswordHash), []byte(body.Password)); err != nil {
		e.logger.InfoContext(c.Request.Context(), "login failed", "reason", "invalid_credentials")
		c.JSON(http.StatusBadRequest, gin.H{"error": "email or password is incorrect"})
		return
	}

	generateToken := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"id":    userFound.IdExternal.String(),
		"email": userFound.Email,
		"exp":   time.Now().Add(time.Hour * 24 * 7).Unix(),
	})

	token, err := generateToken.SignedString([]byte(os.Getenv("SECRET")))

	if err != nil {
		e.logger.ErrorContext(c.Request.Context(), "failed to sign JWT", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "An error occured"})
		return
	}

	var secure bool
	sameSite := http.SameSiteDefaultMode
	domain := e.ClientDomain

	if !strings.Contains(domain, "localhost") {
		secure = true
		sameSite = http.SameSiteNoneMode
	}

	e.logger.DebugContext(c.Request.Context(), "setting auth cookie", "secure", secure, "same_site", int(sameSite))

	cookie := &http.Cookie{
		Name:     "Access_Token",
		Value:    token,
		Path:     "/",
		Domain:   domain,
		MaxAge:   604800,
		Secure:   secure,
		HttpOnly: true,
		SameSite: sameSite,
	}
	http.SetCookie(c.Writer, cookie)

	c.JSON(200, gin.H{
		"message": "success",
	})
}

// Signup godoc
//
//	@Summary		Signup a new user
//	@Description	Creates a new user account
//	@Tags			auth
//	@Accept			json
//	@Produce		json
//	@Param			userInput	body		signUpInput				true	"User details"
//	@Success		201			{object}	map[string]interface{}	"Account created successfully"
//	@Failure		400			{object}	map[string]interface{}	"Bad request"
//	@Failure		500			{object}	map[string]interface{}	"Internal server error"
//	@Router			/signup [post]
func (e *Endpoint) Signup(c *gin.Context) {
	var body signUpInput

	if err := c.ShouldBindJSON(&body); err != nil {
		e.logger.WarnContext(c.Request.Context(), "failed to bind JSON", "handler", "Signup", "error", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	var userFound model.User
	if err := e.DB.Where("email = ?", body.Email).First(&userFound).Error; err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			e.logger.ErrorContext(c.Request.Context(), "failed to look up user for signup", "error", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "An error occurred"})
			return
		}
	} else {
		e.logger.WarnContext(c.Request.Context(), "signup rejected", "reason", "email_taken")
		c.JSON(http.StatusBadRequest, gin.H{"error": "email taken"})
		return
	}

	passwordHash, err := bcrypt.GenerateFromPassword([]byte(body.Password), bcrypt.DefaultCost)
	if err != nil {
		e.logger.ErrorContext(c.Request.Context(), "failed to hash password for signup", "error", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	user := model.User{
		Email:        body.Email,
		FirstName:    body.FirstName,
		LastName:     body.LastName,
		PasswordHash: string(passwordHash),
	}

	tx := e.DB.Begin()
	if tx.Error != nil {
		e.logger.ErrorContext(c.Request.Context(), "failed to begin signup transaction", "error", tx.Error)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "An error occurred"})
		return
	}

	if err := tx.Create(&user).Error; err != nil {
		_ = tx.Rollback()
		e.logger.ErrorContext(c.Request.Context(), "failed to create user", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("error creating user: %s", err)})
		return
	}

	jobApplicationProfile := model.JobApplicationProfile{
		UserId:    user.IdUser,
		FirstName: body.FirstName,
		LastName:  body.LastName,
		Email:     body.Email,
	}

	if err := tx.Create(&jobApplicationProfile).Error; err != nil {
		_ = tx.Rollback()
		e.logger.ErrorContext(c.Request.Context(), "failed to create job application profile", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("error creating job application profile: %s", err)})
		return
	}

	if err := tx.Commit().Error; err != nil {
		e.logger.ErrorContext(c.Request.Context(), "failed to commit signup transaction", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("error committing transaction: %s", err)})
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"message": "account created successfully",
		"data":    body,
	})
}

// Logout godoc
//
//	@Summary		Logout user
//	@Description	Logs out the user by clearing the access token
//	@Tags			auth
//	@Success		200	{object}	map[string]interface{}	"Logout successful"
//	@Router			/logout [post]
func (e *Endpoint) Logout(c *gin.Context) {
	// Set cookie with all parameters to ensure proper deletion
	var secure bool
	sameSite := http.SameSiteDefaultMode
	domain := e.ClientDomain

	if !strings.Contains(domain, "localhost") {
		secure = true
		sameSite = http.SameSiteNoneMode
	}

	cookie := &http.Cookie{
		Name:     "Access_Token",
		Value:    "",
		Path:     "/",
		Domain:   domain,
		MaxAge:   -1,
		Secure:   secure,
		HttpOnly: true,
		SameSite: sameSite,
	}
	http.SetCookie(c.Writer, cookie)

	c.JSON(http.StatusOK, gin.H{"message": "logout successful"})
}

// ResetPassword godoc
//
//	@Summary		Reset user password
//	@Description	Resets the password for the authenticated user
//	@Tags			auth
//	@Accept			json
//	@Produce		json
//	@Param			passwordResetRequest	body		passwordResetRequest	true	"Password reset details"
//	@Success		200						{object}	map[string]interface{}	"Password updated successfully"
//	@Failure		400						{object}	map[string]interface{}	"Bad request"
//	@Failure		401						{object}	map[string]interface{}	"Unauthorized"
//	@Failure		500						{object}	map[string]interface{}	"Internal server error"
//	@Router			/reset-password [post]
func (e *Endpoint) ResetPassword(c *gin.Context) {
	var body passwordResetRequest

	if err := c.ShouldBindJSON(&body); err != nil {
		e.logger.WarnContext(c.Request.Context(), "failed to bind JSON", "handler", "ResetPassword", "error", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Get the current user from the context
	user, ok := c.Value("currentUser").(model.User)
	if !ok {
		e.logger.InfoContext(c.Request.Context(), "unauthorized", "handler", "ResetPassword", "reason", "missing_current_user")
		c.JSON(http.StatusUnauthorized, gin.H{"error": "User not authenticated"})
		return
	}

	// Verify the current password
	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(body.Password)); err != nil {
		e.logger.WarnContext(c.Request.Context(), "reset password rejected", "reason", "incorrect_current_password")
		c.JSON(http.StatusBadRequest, gin.H{"error": "Current password is incorrect"})
		return
	}

	// Hash the new password
	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(body.NewPassword), bcrypt.DefaultCost)
	if err != nil {
		e.logger.ErrorContext(c.Request.Context(), "failed to hash new password", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to hash new password"})
		return
	}

	// Update the password in the database
	user.PasswordHash = string(hashedPassword)
	if err := e.DB.Save(&user).Error; err != nil {
		e.logger.ErrorContext(c.Request.Context(), "failed to update password", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update password"})
		return
	}

	var secure bool
	domain := e.ClientDomain

	// Strip any protocol prefix from domain
	domain = strings.TrimPrefix(domain, "http://")
	domain = strings.TrimPrefix(domain, "https://")
	c.SetCookie("Access_Token", "", -1, "/", domain, secure, true)
	c.JSON(http.StatusOK, gin.H{"message": "Password updated successfully. Please login with new password"})
}

type GetUserResponse struct {
	Id                         string     `json:"id"`
	Email                      string     `json:"email"`
	FirstName                  string     `json:"firstName"`
	LastName                   string     `json:"lastName"`
	IsOnboardingComplete       bool       `json:"isOnboardingComplete"`
	IsResumeOnboardingComplete bool       `json:"isResumeOnboardingComplete"`
	CreatedAt                  time.Time  `json:"createdAt"`
	UpdatedAt                  time.Time  `json:"updatedAt"`
	DeletedAt                  *time.Time `json:"deletedAt"`
	IsAdmin                    bool       `json:"isAdmin"`
}

// GetCurrentUser godoc
//
//	@Summary		Get current user
//	@Description	Retrieves the current authenticated user's information
//	@Tags			users
//	@Success		200	{object}	model.User				"Current user data"
//	@Failure		500	{object}	map[string]interface{}	"Internal server error"
//	@Router			/me [get]
func (e *Endpoint) GetCurrentUser(c *gin.Context) {
	user, ok := c.Value("currentUser").(model.User)
	if !ok {
		e.logger.InfoContext(c.Request.Context(), "currentUser missing from context", "handler", "GetCurrentUser")
		c.JSON(http.StatusUnauthorized, gin.H{"error": "We couldn't retrieve your data"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": GetUserResponse{
		Id:                         user.IdExternal.String(),
		Email:                      user.Email,
		FirstName:                  user.FirstName,
		LastName:                   user.LastName,
		IsOnboardingComplete:       e.isOnboardingComplete(user.IdUser),
		IsResumeOnboardingComplete: e.isResumeOnboardingComplete(user.IdUser),
		CreatedAt:                  user.CreatedAt,
		UpdatedAt:                  user.UpdatedAt,
		DeletedAt:                  user.DeletedAt,
		IsAdmin:                    user.IsAdmin,
	}})
}

// isOnboardingComplete returns false if the user has no job_application_profile
// or if any profile field required for onboarding is empty.
func (e *Endpoint) isOnboardingComplete(userID uint) bool {
	var profile model.JobApplicationProfile
	if err := e.DB.Where("id_user = ?", userID).First(&profile).Error; err != nil {
		return false
	}

	if profile.FirstName == "" || profile.LastName == "" || profile.Email == "" ||
		profile.Phone == "" || profile.Address == "" || profile.City == "" ||
		profile.State == "" || profile.Zip == "" || profile.CountryOfResidence == "" ||
		profile.Gender == "" || profile.DateOfBirth.IsZero() ||
		len(profile.CountriesOfCitizenship) == 0 {
		return false
	}

	// Required onboarding fields (extra)
	if profile.IsOpenToRelocating == nil || profile.NoticePeriodDays == nil ||
		len(profile.PreferredWorkingArrangement) == 0 ||
		len(profile.LanguageProficiencies) == 0 ||
		profile.LinkedInUrl == nil {
		return false
	}

	for _, lp := range profile.LanguageProficiencies {
		if lp.Language == "" || lp.Proficiency == "" {
			return false
		}
	}

	if *profile.NoticePeriodDays < 0 {
		return false
	}
	return true
}

func (e *Endpoint) isResumeOnboardingComplete(userID uint) bool {
	var resume model.Resume
	if err := e.DB.Unscoped().Where("id_user = ?", userID).First(&resume).Error; err != nil {
		return false
	}
	return true
}
