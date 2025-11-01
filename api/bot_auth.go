package api

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
)

// BotAuthConfig Bot认证配置
type BotAuthConfig struct {
	BotToken      string
	ApiSecret     string
	MaxTimeDrift  int64 // 最大时间差（秒）
}

// BotAuthMiddleware Bot认证中间件
func BotAuthMiddleware(config BotAuthConfig) gin.HandlerFunc {
	return func(c *gin.Context) {
		// 验证Bot Token
		botToken := c.GetHeader("X-Bot-Token")
		if botToken != config.BotToken {
			c.JSON(http.StatusUnauthorized, gin.H{
				"error": "Invalid bot token",
			})
			c.Abort()
			return
		}

		// 验证时间戳
		timestampStr := c.GetHeader("X-Bot-Timestamp")
		if timestampStr == "" {
			c.JSON(http.StatusUnauthorized, gin.H{
				"error": "Missing timestamp",
			})
			c.Abort()
			return
		}

		timestamp, err := strconv.ParseInt(timestampStr, 10, 64)
		if err != nil {
			c.JSON(http.StatusUnauthorized, gin.H{
				"error": "Invalid timestamp format",
			})
			c.Abort()
			return
		}

		// 检查时间戳是否在允许范围内（防重放攻击）
		currentTime := time.Now().Unix()
		if abs64(currentTime-timestamp) > config.MaxTimeDrift {
			c.JSON(http.StatusUnauthorized, gin.H{
				"error": "Request timestamp too old or too far in the future",
			})
			c.Abort()
			return
		}

		// 验证签名
		signature := c.GetHeader("X-Bot-Signature")
		if signature == "" {
			c.JSON(http.StatusUnauthorized, gin.H{
				"error": "Missing signature",
			})
			c.Abort()
			return
		}

		// 获取请求体
		body := c.GetString("body")
		if body == "" {
			// 如果body为空，尝试读取
			if c.Request.Body != nil {
				bodyBytes, _ := c.GetRawData()
				body = string(bodyBytes)
				// 重置body供后续使用
				c.Request.Body = newRequestBodyReader(bodyBytes)
			}
		}

		// 计算期望的签名
		expectedSignature := generateSignature(config.ApiSecret, timestamp, body)
		if !hmac.Equal([]byte(signature), []byte(expectedSignature)) {
			c.JSON(http.StatusUnauthorized, gin.H{
				"error": "Invalid signature",
			})
			c.Abort()
			return
		}

		// 提取Telegram User ID（如果存在）
		telegramUserID := c.GetHeader("X-Telegram-User-ID")
		if telegramUserID != "" {
			c.Set("telegram_user_id", telegramUserID)
			log.Printf("Bot request from Telegram User ID: %s", telegramUserID)
		}

		// 认证通过，继续处理请求
		c.Next()
	}
}

// generateSignature 生成HMAC签名
func generateSignature(secret string, timestamp int64, body string) string {
	data := fmt.Sprintf("%d%s", timestamp, body)
	h := hmac.New(sha256.New, []byte(secret))
	h.Write([]byte(data))
	return hex.EncodeToString(h.Sum(nil))
}

// abs64 计算int64的绝对值
func abs64(x int64) int64 {
	if x < 0 {
		return -x
	}
	return x
}

// newRequestBodyReader 创建新的请求体读取器
func newRequestBodyReader(data []byte) *requestBodyReader {
	return &requestBodyReader{
		data: data,
		pos:  0,
	}
}

type requestBodyReader struct {
	data []byte
	pos  int
}

func (r *requestBodyReader) Read(p []byte) (n int, err error) {
	if r.pos >= len(r.data) {
		return 0, io.EOF
	}
	n = copy(p, r.data[r.pos:])
	r.pos += n
	return n, nil
}

func (r *requestBodyReader) Close() error {
	return nil
}

// GetBotAuthConfig 从环境变量获取Bot认证配置
func GetBotAuthConfig() BotAuthConfig {
	config := BotAuthConfig{
		BotToken:     os.Getenv("BOT_API_TOKEN"),
		ApiSecret:    os.Getenv("BOT_API_SECRET"),
		MaxTimeDrift: 300, // 默认5分钟
	}

	if config.BotToken == "" {
		log.Fatal("BOT_API_TOKEN environment variable is required")
	}
	if config.ApiSecret == "" {
		log.Fatal("BOT_API_SECRET environment variable is required")
	}

	// 如果设置了最大时间差环境变量，使用它
	if maxDriftStr := os.Getenv("BOT_MAX_TIME_DRIFT"); maxDriftStr != "" {
		if maxDrift, err := strconv.ParseInt(maxDriftStr, 10, 64); err == nil {
			config.MaxTimeDrift = maxDrift
		}
	}

	return config
}