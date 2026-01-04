package mcp

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

// Provider AI提供商类型
type Provider string

const (
	ProviderDeepSeek Provider = "deepseek"
	ProviderQwen     Provider = "qwen"
	ProviderCustom   Provider = "custom"
)

// Client AI API配置
type Client struct {
	Provider   Provider
	APIKey     string
	BaseURL    string
	Model      string
	Timeout    time.Duration
	UseFullURL bool // 是否使用完整URL（不添加/chat/completions）
	MaxTokens  int  // AI响应的最大token数
}

func New() *Client {
	// 从环境变量读取 MaxTokens，默认 2000
	maxTokens := 2000
	if envMaxTokens := os.Getenv("AI_MAX_TOKENS"); envMaxTokens != "" {
		if parsed, err := strconv.Atoi(envMaxTokens); err == nil && parsed > 0 {
			maxTokens = parsed
			log.Printf("🔧 [MCP] 使用环境变量 AI_MAX_TOKENS: %d", maxTokens)
		} else {
			log.Printf("⚠️  [MCP] 环境变量 AI_MAX_TOKENS 无效 (%s)，使用默认值: %d", envMaxTokens, maxTokens)
		}
	}

	// 从环境变量读取 Timeout，默认 120s
	timeout := 120 * time.Second
	if envTimeout := os.Getenv("AI_TIMEOUT_SECONDS"); envTimeout != "" {
		if parsed, err := strconv.Atoi(envTimeout); err == nil && parsed > 0 {
			timeout = time.Duration(parsed) * time.Second
			log.Printf("🔧 [MCP] 使用环境变量 AI_TIMEOUT_SECONDS: %ds", parsed)
		} else {
			log.Printf("⚠️  [MCP] 环境变量 AI_TIMEOUT_SECONDS 无效 (%s)，使用默认值: %v", envTimeout, timeout)
		}
	}

	// 默认配置
	return &Client{
		Provider:  ProviderDeepSeek,
		BaseURL:   "https://api.deepseek.com/v1",
		Model:     "",
		Timeout:   timeout, // 包含整个请求（含流式读取 body）的超时
		MaxTokens: maxTokens,
	}
}

// SetDeepSeekAPIKey 设置DeepSeek API密钥
// customURL 为空时使用默认URL，customModel 为空时使用默认模型
func (client *Client) SetDeepSeekAPIKey(apiKey string, customURL string, customModel string) {
	client.Provider = ProviderDeepSeek
	client.APIKey = apiKey
	if customURL != "" {
		client.BaseURL = customURL
		log.Printf("🔧 [MCP] DeepSeek 使用自定义 BaseURL: %s", customURL)
	} else {
		client.BaseURL = "https://api.deepseek.com/v1"
		log.Printf("🔧 [MCP] DeepSeek 使用默认 BaseURL: %s", client.BaseURL)
	}
	if customModel != "" {
		normalizedModel := strings.TrimSpace(customModel)
		if strings.EqualFold(normalizedModel, "deepseek") {
			normalizedModel = "deepseek-reasoner"
			log.Printf("🔧 [MCP] DeepSeek 自动修正模型名: %s -> %s", customModel, normalizedModel)
		}
		client.Model = normalizedModel
		log.Printf("🔧 [MCP] DeepSeek 使用自定义 Model: %s", normalizedModel)
	} else {
		client.Model = "deepseek-reasoner"
		log.Printf("🔧 [MCP] DeepSeek 使用默认 Model: %s", client.Model)
	}
	// deepseek-reasoner 会额外产生 reasoning_content，且通常需要更多 max_tokens 才能输出最终 content
	// 仅在用户未显式设置 AI_MAX_TOKENS 且仍为默认值时，提升默认 token 上限
	if os.Getenv("AI_MAX_TOKENS") == "" && client.Model == "deepseek-reasoner" && client.MaxTokens == 2000 {
		client.MaxTokens = 8000
		log.Printf("🔧 [MCP] DeepSeek %s 自动提高 MaxTokens: %d", client.Model, client.MaxTokens)
	}
	// deepseek-reasoner 推理更耗时：在未显式设置超时时，适当提高默认超时，避免流式读取中断
	if os.Getenv("AI_TIMEOUT_SECONDS") == "" && client.Model == "deepseek-reasoner" && client.Timeout == 120*time.Second {
		client.Timeout = 300 * time.Second
		log.Printf("🔧 [MCP] DeepSeek %s 自动提高 Timeout: %v", client.Model, client.Timeout)
	}
	// 避免在日志中泄露密钥内容（只记录长度用于排查）
	if apiKey != "" {
		log.Printf("🔧 [MCP] DeepSeek API Key 已设置 (len=%d)", len(apiKey))
	}
}

// SetQwenAPIKey 设置阿里云Qwen API密钥
// customURL 为空时使用默认URL，customModel 为空时使用默认模型
func (client *Client) SetQwenAPIKey(apiKey string, customURL string, customModel string) {
	client.Provider = ProviderQwen
	client.APIKey = apiKey
	if customURL != "" {
		client.BaseURL = customURL
		log.Printf("🔧 [MCP] Qwen 使用自定义 BaseURL: %s", customURL)
	} else {
		client.BaseURL = "https://dashscope.aliyuncs.com/compatible-mode/v1"
		log.Printf("🔧 [MCP] Qwen 使用默认 BaseURL: %s", client.BaseURL)
	}
	if customModel != "" {
		client.Model = customModel
		log.Printf("🔧 [MCP] Qwen 使用自定义 Model: %s", customModel)
	} else {
		client.Model = "qwen3-30b"
		log.Printf("🔧 [MCP] Qwen 使用默认 Model: %s", client.Model)
	}
	// 避免在日志中泄露密钥内容（只记录长度用于排查）
	if apiKey != "" {
		log.Printf("🔧 [MCP] Qwen API Key 已设置 (len=%d)", len(apiKey))
	}
}

// SetCustomAPI 设置自定义OpenAI兼容API
func (client *Client) SetCustomAPI(apiURL, apiKey, modelName string) {
	client.Provider = ProviderCustom
	client.APIKey = apiKey

	// 检查URL是否以#结尾，如果是则使用完整URL（不添加/chat/completions）
	if strings.HasSuffix(apiURL, "#") {
		client.BaseURL = strings.TrimSuffix(apiURL, "#")
		client.UseFullURL = true
	} else {
		client.BaseURL = apiURL
		client.UseFullURL = false
	}

	client.Model = modelName
	client.Timeout = 120 * time.Second
}

// SetClient 设置完整的AI配置（高级用户）
func (client *Client) SetClient(Client Client) {
	if Client.Timeout == 0 {
		Client.Timeout = 30 * time.Second
	}
	client = &Client
}

// CallWithMessages 使用 system + user prompt 调用AI API（推荐）
func (client *Client) CallWithMessages(systemPrompt, userPrompt string) (string, error) {
	if client.APIKey == "" {
		return "", fmt.Errorf("AI API密钥未设置，请先调用 SetDeepSeekAPIKey() 或 SetQwenAPIKey()")
	}

	// 重试配置
	maxRetries := 3
	var lastErr error
	fallbackToChatUsed := false

	for attempt := 1; attempt <= maxRetries; attempt++ {
		if attempt > 1 {
			fmt.Printf("⚠️  AI API调用失败，正在重试 (%d/%d)...\n", attempt, maxRetries)
		}

		result, err := client.callOnce(systemPrompt, userPrompt)
		if err == nil {
			if attempt > 1 {
				fmt.Printf("✓ AI API重试成功\n")
			}
			return result, nil
		}

		lastErr = err

		// DeepSeek reasoner 常见问题：只生成 reasoning_content，未生成最终 content（通常是 max_tokens 不够）
		var roe *reasoningOnlyError
		if errors.As(err, &roe) && client.Provider == ProviderDeepSeek && strings.EqualFold(strings.TrimSpace(client.Model), "deepseek-reasoner") {
			before := client.MaxTokens
			// 逐步提高 max_tokens，尽量在不无限膨胀的前提下拿到最终 content
			const capTokens = 32000
			next := before
			switch {
			case before < 16000:
				next = 16000
			case before < capTokens:
				next = before * 2
				if next > capTokens {
					next = capTokens
				}
			}
			if next > before {
				client.MaxTokens = next
				log.Printf("🔧 [MCP] DeepSeek %s 检测到仅 reasoning_content，自动提高 MaxTokens: %d -> %d (finish_reason=%s)", client.Model, before, next, roe.FinishReason)

				// 若用户未显式设置超时，则随 token 上限同步放宽读取时间，避免再次超时
				if os.Getenv("AI_TIMEOUT_SECONDS") == "" {
					if client.Timeout < 10*time.Minute && next >= 16000 {
						client.Timeout = 10 * time.Minute
						log.Printf("🔧 [MCP] DeepSeek %s 自动提高 Timeout: %v", client.Model, client.Timeout)
					}
				}
				// 立即进入下一轮重试（不等待），因为这类错误通常不是网络抖动
				continue
			}

			// 已到达 token 上限仍无 content：最后兜底回退到 deepseek-chat
			if !fallbackToChatUsed {
				fallbackToChatUsed = true
				originalModel := client.Model
				client.Model = "deepseek-chat"
				log.Printf("⚠️  [MCP] DeepSeek %s 仍未生成 content，回退模型: %s -> %s", originalModel, originalModel, client.Model)
				fallbackResult, fallbackErr := client.callOnce(systemPrompt, userPrompt)
				client.Model = originalModel
				if fallbackErr == nil {
					return fallbackResult, nil
				}
				lastErr = fmt.Errorf("reasoner无content且回退deepseek-chat失败: %w", fallbackErr)
			}
		}

		// 如果不是网络错误，不重试
		if !isRetryableError(err) {
			return "", err
		}

		// 重试前等待
		if attempt < maxRetries {
			waitTime := time.Duration(attempt) * 2 * time.Second
			fmt.Printf("⏳ 等待%v后重试...\n", waitTime)
			time.Sleep(waitTime)
		}
	}

	return "", fmt.Errorf("重试%d次后仍然失败: %w", maxRetries, lastErr)
}

type reasoningOnlyError struct {
	Provider       Provider
	Model          string
	MaxTokens      int
	FinishReason   string
	ReasoningBytes int
}

func (e *reasoningOnlyError) Error() string {
	finish := e.FinishReason
	if finish == "" {
		finish = "unknown"
	}
	return fmt.Sprintf("流式响应无最终内容(content)，仅返回 reasoning_content；finish_reason=%s；可能是 max_tokens 太小导致未生成最终答案。建议增大 AI_MAX_TOKENS（当前 %d），或改用 deepseek-chat 以减少推理占用。", finish, e.MaxTokens)
}

// callOnce 单次调用AI API（内部使用）
func (client *Client) callOnce(systemPrompt, userPrompt string) (string, error) {
	// 打印当前 AI 配置
	log.Printf("📡 [MCP] AI 请求配置:")
	log.Printf("   Provider: %s", client.Provider)
	log.Printf("   BaseURL: %s", client.BaseURL)
	log.Printf("   Model: %s", client.Model)
	log.Printf("   UseFullURL: %v", client.UseFullURL)
	if client.APIKey != "" {
		log.Printf("   API Key: (len=%d)", len(client.APIKey))
	}

	// 构建 messages 数组
	messages := []map[string]string{}

	// 如果有 system prompt，添加 system message
	if systemPrompt != "" {
		messages = append(messages, map[string]string{
			"role":    "system",
			"content": systemPrompt,
		})
	}

	// 添加 user message
	messages = append(messages, map[string]string{
		"role":    "user",
		"content": userPrompt,
	})

	// 构建请求体
	requestBody := map[string]interface{}{
		"model":       client.Model,
		"messages":    messages,
		"temperature": 0.5, // 降低temperature以提高JSON格式稳定性
		"max_tokens":  client.MaxTokens,
		"stream":      true, // 启用流式，减少长响应截断概率
	}

	// 注意：response_format 参数仅 OpenAI 支持，DeepSeek/Qwen 不支持
	// 我们通过强化 prompt 和后处理来确保 JSON 格式正确

	jsonData, err := json.Marshal(requestBody)
	if err != nil {
		return "", fmt.Errorf("序列化请求失败: %w", err)
	}

	// 创建HTTP请求
	var url string
	if client.UseFullURL {
		// 使用完整URL，不添加/chat/completions
		url = client.BaseURL
	} else {
		// 默认行为：添加/chat/completions
		url = fmt.Sprintf("%s/chat/completions", client.BaseURL)
	}
	log.Printf("📡 [MCP] 请求 URL: %s", url)

	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		return "", fmt.Errorf("创建请求失败: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	// 根据不同的Provider设置认证方式
	switch client.Provider {
	case ProviderDeepSeek:
		req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", client.APIKey))
	case ProviderQwen:
		// 阿里云Qwen使用API-Key认证
		req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", client.APIKey))
		// 注意：如果使用的不是兼容模式，可能需要不同的认证方式
	default:
		req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", client.APIKey))
	}

	// 发送请求
	httpClient := &http.Client{Timeout: client.Timeout}
	resp, err := httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("发送请求失败: %w", err)
	}
	defer resp.Body.Close()

	// 读取流式响应
	content, err := readStreamContent(resp, client.Provider, client.Model, client.MaxTokens)
	if err != nil {
		return "", err
	}

	return content, nil
}

// isRetryableError 判断错误是否可重试
func isRetryableError(err error) bool {
	errStr := err.Error()
	// 网络错误、超时、EOF等可以重试
	retryableErrors := []string{
		"EOF",
		"timeout",
		"context deadline exceeded",
		"Client.Timeout",
		"connection reset",
		"connection refused",
		"temporary failure",
		"no such host",
		"stream error",   // HTTP/2 stream 错误
		"INTERNAL_ERROR", // 服务端内部错误
	}
	for _, retryable := range retryableErrors {
		if strings.Contains(errStr, retryable) {
			return true
		}
	}
	return false
}

// readStreamContent 解析 OpenAI/DeepSeek 兼容的流式响应
func readStreamContent(resp *http.Response, provider Provider, model string, maxTokens int) (string, error) {
	defer resp.Body.Close()

	// 提前处理非200状态码
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("API返回错误 (status %d): %s", resp.StatusCode, string(body))
	}

	scanner := bufio.NewScanner(resp.Body)
	// 增加单行大小，避免长增量被截断
	const maxLine = 8 * 1024 * 1024
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, maxLine)

	var contentSB strings.Builder
	var reasoningSB strings.Builder
	lastFinishReason := ""
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "[DONE]" {
			break
		}

		var chunk struct {
			Error *struct {
				Message string `json:"message"`
				Type    string `json:"type"`
				Code    string `json:"code"`
			} `json:"error"`
			Choices []struct {
				Delta struct {
					Content          string `json:"content"`
					ReasoningContent string `json:"reasoning_content"`
				} `json:"delta"`
				Message struct {
					Content          string `json:"content"`
					ReasoningContent string `json:"reasoning_content"`
				} `json:"message"`
				FinishReason string `json:"finish_reason"`
			} `json:"choices"`
		}

		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			return "", fmt.Errorf("解析流式分片失败: %w", err)
		}

		if chunk.Error != nil && chunk.Error.Message != "" {
			return "", fmt.Errorf("API流式错误: %s", chunk.Error.Message)
		}

		for _, choice := range chunk.Choices {
			if choice.FinishReason != "" {
				lastFinishReason = choice.FinishReason
			}
			if choice.Delta.ReasoningContent != "" {
				reasoningSB.WriteString(choice.Delta.ReasoningContent)
			} else if choice.Message.ReasoningContent != "" {
				reasoningSB.WriteString(choice.Message.ReasoningContent)
			}
			if choice.Delta.Content != "" {
				contentSB.WriteString(choice.Delta.Content)
			} else if choice.Message.Content != "" {
				contentSB.WriteString(choice.Message.Content)
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("读取流式响应失败: %w", err)
	}

	result := contentSB.String()
	if result == "" {
		if provider == ProviderDeepSeek && strings.EqualFold(strings.TrimSpace(model), "deepseek-reasoner") && reasoningSB.Len() > 0 {
			return "", &reasoningOnlyError{
				Provider:       provider,
				Model:          model,
				MaxTokens:      maxTokens,
				FinishReason:   lastFinishReason,
				ReasoningBytes: reasoningSB.Len(),
			}
		}
		return "", fmt.Errorf("流式响应为空")
	}
	return result, nil
}
