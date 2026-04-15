package costtracking

import (
	"errors"
	"net/http"
	"time"

	"github.com/SomtoJF/iris-api/model"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

type Endpoint struct {
	db *gorm.DB
}

func NewEndpoint(db *gorm.DB) *Endpoint {
	return &Endpoint{db: db}
}

type user struct {
	Id    string `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
}

type jobApplication struct {
	Id          string `json:"id"`
	Title       string `json:"title"`
	CompanyName string `json:"company_name"`
}

type CostTracking struct {
	Id             string          `json:"id"`
	Type           string          `json:"type"`
	User           user            `json:"user"`
	JobApplication *jobApplication `json:"job_application,omitempty"`
	Model          string          `json:"model"`
	InputTokens    int             `json:"input_tokens"`
	OutputTokens   int             `json:"output_tokens"`
	InputCost      float64         `json:"input_cost"`
	OutputCost     float64         `json:"output_cost"`
	TotalCost      float64         `json:"total_cost"`
	CreatedAt      time.Time       `json:"created_at"`
	UpdatedAt      time.Time       `json:"updated_at"`
}

type GetCostTrackingResponse struct {
	Data                 []CostTracking `json:"data"`
	TotalAccumulatedCost float64        `json:"total_accumulated_cost"`
	Total                int            `json:"total"`
	Page                 int            `json:"page"`
	Limit                int            `json:"limit"`
}

type getCostTrackingRequest struct {
	Page             int    `form:"page" binding:"required"`
	Limit            int    `form:"limit" binding:"required"`
	UserID           string `form:"user_id"`
	JobApplicationID string `form:"job_application_id"`
}

type costTrackingFilters struct {
	userID           *uint
	jobApplicationID *uint
}

func emptyCostTrackingList(req getCostTrackingRequest) GetCostTrackingResponse {
	return GetCostTrackingResponse{
		Data:                 []CostTracking{},
		TotalAccumulatedCost: 0,
		Total:                0,
		Page:                 req.Page,
		Limit:                req.Limit,
	}
}

func (e *Endpoint) costTrackingQuery(f costTrackingFilters) *gorm.DB {
	q := e.db.Model(&model.CostTracking{})
	if f.userID != nil {
		q = q.Where("cost_tracking.id_user = ?", *f.userID)
	}
	if f.jobApplicationID != nil {
		q = q.Where("cost_tracking.id_job_application = ?", *f.jobApplicationID)
	}
	return q
}

func (e *Endpoint) GetCostTracking(c *gin.Context) {
	currentUser, ok := c.Value("currentUser").(model.User)
	if !ok || !currentUser.IsAdmin {
		c.JSON(http.StatusForbidden, gin.H{"error": "Forbidden"})
		return
	}

	var req getCostTrackingRequest
	if err := c.ShouldBindQuery(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	filters := costTrackingFilters{}
	if req.JobApplicationID != "" {
		jobUUID, err := uuid.Parse(req.JobApplicationID)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid job_application_id"})
			return
		}
		var job model.JobApplication
		if err := e.db.Where("id_external = ?", jobUUID).First(&job).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				c.JSON(http.StatusOK, emptyCostTrackingList(req))
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to resolve job application"})
			return
		}
		id := job.IdJobApplication
		filters.jobApplicationID = &id
	}
	if req.UserID != "" {
		userUUID, err := uuid.Parse(req.UserID)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid user_id"})
			return
		}
		var u model.User
		if err := e.db.Where("id_external = ?", userUUID).First(&u).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				c.JSON(http.StatusOK, emptyCostTrackingList(req))
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to resolve user"})
			return
		}
		id := u.IdUser
		filters.userID = &id
	}

	var total int64
	if err := e.costTrackingQuery(filters).Count(&total).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to count records"})
		return
	}

	var totalAccumulatedCost float64
	if err := e.costTrackingQuery(filters).
		Select("COALESCE(SUM(cost_tracking.total_cost), 0)").
		Scan(&totalAccumulatedCost).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to sum costs"})
		return
	}

	var records []model.CostTracking
	err := e.costTrackingQuery(filters).
		Preload("User").
		Preload("JobApplication").
		Order("cost_tracking.created_at DESC").
		Limit(req.Limit).
		Offset((req.Page - 1) * req.Limit).
		Find(&records).Error
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch records"})
		return
	}

	data := make([]CostTracking, len(records))
	for i, r := range records {
		data[i] = mapCostTracking(r)
	}

	c.JSON(http.StatusOK, GetCostTrackingResponse{
		Data:                 data,
		TotalAccumulatedCost: totalAccumulatedCost,
		Total:                int(total),
		Page:                 req.Page,
		Limit:                req.Limit,
	})
}

func mapCostTracking(r model.CostTracking) CostTracking {
	ct := CostTracking{
		Id:         r.IdExternal.String(),
		Type:       string(r.Type),
		User:       mapUser(r.User),
		OutputCost: r.OutputCost,
		TotalCost:  r.TotalCost,
		CreatedAt:  r.CreatedAt,
		UpdatedAt:  r.UpdatedAt,
	}

	if r.Model != nil {
		ct.Model = *r.Model
	}
	if r.InputTokens != nil {
		ct.InputTokens = *r.InputTokens
	}
	if r.OutputTokens != nil {
		ct.OutputTokens = *r.OutputTokens
	}
	if r.InputCost != nil {
		ct.InputCost = *r.InputCost
	}
	if r.JobApplication != nil {
		ct.JobApplication = &jobApplication{
			Id:          r.JobApplication.IdExternal.String(),
			Title:       r.JobApplication.JobTitle,
			CompanyName: r.JobApplication.CompanyName,
		}
	}

	return ct
}

func mapUser(u model.User) user {
	return user{
		Id:    u.IdExternal.String(),
		Name:  u.FirstName + " " + u.LastName,
		Email: u.Email,
	}
}

type SearchCostEntitiesResponse struct {
	Users           []user           `json:"users"`
	JobApplications []jobApplication `json:"job_applications"`
}

func (e *Endpoint) SearchCostEntities(c *gin.Context) {
	currentUser, ok := c.Value("currentUser").(model.User)
	if !ok || !currentUser.IsAdmin {
		c.JSON(http.StatusForbidden, gin.H{"error": "Forbidden"})
		return
	}

	q := c.Query("q")
	if q == "" {
		c.JSON(http.StatusOK, SearchCostEntitiesResponse{
			Users:           []user{},
			JobApplications: []jobApplication{},
		})
		return
	}

	like := "%" + q + "%"

	var users []model.User
	e.db.Where("first_name LIKE ? OR last_name LIKE ? OR email LIKE ?", like, like, like).
		Limit(5).
		Find(&users)

	var jobApps []model.JobApplication
	e.db.Where("job_title LIKE ? OR company_name LIKE ?", like, like).
		Limit(5).
		Find(&jobApps)

	usersResult := make([]user, len(users))
	for i, u := range users {
		usersResult[i] = mapUser(u)
	}

	jobAppsResult := make([]jobApplication, len(jobApps))
	for i, j := range jobApps {
		jobAppsResult[i] = jobApplication{
			Id:          j.IdExternal.String(),
			Title:       j.JobTitle,
			CompanyName: j.CompanyName,
		}
	}

	c.JSON(http.StatusOK, SearchCostEntitiesResponse{
		Users:           usersResult,
		JobApplications: jobAppsResult,
	})
}
