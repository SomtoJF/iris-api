package verifyauth

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/SomtoJF/iris-api/model"
	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt"
	"gorm.io/gorm"
)

type ctxKeyUserEmail struct{}

func UserEmailFromContext(ctx context.Context) (string, bool) {
	v := ctx.Value(ctxKeyUserEmail{})
	email, ok := v.(string)
	return email, ok && email != ""
}

func withUserEmail(ctx context.Context, email string) context.Context {
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" {
		return ctx
	}
	return context.WithValue(ctx, ctxKeyUserEmail{}, email)
}

type Middleware struct {
	DB *gorm.DB
}

func NewMiddleware(db *gorm.DB) *Middleware {
	return &Middleware{DB: db}
}

func (m *Middleware) VerifyAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		tokenString, err := c.Cookie("Access_Token")
		if err != nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
			c.Abort()
			return
		}

		token, err := jwt.Parse(tokenString, func(token *jwt.Token) (interface{}, error) {
			if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
			}
			return []byte(os.Getenv("SECRET")), nil
		})

		if err != nil || !token.Valid {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid or expired token"})
			c.Abort()
			return
		}

		claims, ok := token.Claims.(jwt.MapClaims)
		if !ok {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid token"})
			c.Abort()
			return
		}

		if float64(time.Now().Unix()) > claims["exp"].(float64) {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "token expired"})
			c.AbortWithStatus(http.StatusUnauthorized)
			return
		}

		var user model.User
		result := m.DB.Where("id_external = ?", claims["id"]).First(&user)
		if result.Error != nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "User not found"})
			c.Abort()
			return
		}

		c.Request = c.Request.WithContext(withUserEmail(c.Request.Context(), user.Email))
		c.Set("currentUser", user)
		c.Set("userId", user.IdUser)

		c.Next()
	}
}
