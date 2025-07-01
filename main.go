package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/joho/godotenv"
	"github.com/sashabaranov/go-openai"
)

type fn func(map[string]any) string

type ChatClient struct {
	openaiClient *openai.Client
	model        string
	messages     []openai.ChatCompletionMessage // 用于存储历史消息，实现多轮对话
	funcs        map[string]fn
}

func main() {
	_ = godotenv.Load()

	apiKey := os.Getenv("OPENAI_API_KEY")
	baseURL := os.Getenv("OPENAI_API_BASE")
	model := os.Getenv("OPENAI_API_MODEL")
	if apiKey == "" || baseURL == "" || model == "" {
		fmt.Println("检查环境变量设置")
		return
	}

	config := openai.DefaultConfig(apiKey)
	config.BaseURL = baseURL
	openaiClient := openai.NewClientWithConfig(config)

	cc := &ChatClient{
		openaiClient: openaiClient,
		model:        model,
		messages:     make([]openai.ChatCompletionMessage, 0),
	}

	// 注册函数到chatClient
	cc.funcs = map[string]fn{
		"getWeather": getWeather,
		"getTime":    getTime,
	}

	cc.ChatLoop()
}

// 外层for循环用于多轮聊天输入
func (cc *ChatClient) ChatLoop() {
	fmt.Print("Type your queries or 'quit' to exit.")
	scanner := bufio.NewScanner(os.Stdin)
	for {
		fmt.Print("\nUser: ")
		if !scanner.Scan() {
			break
		}
		userInput := strings.TrimSpace(scanner.Text())
		if strings.ToLower(userInput) == "quit" {
			break
		}
		if userInput == "" {
			continue
		}

		response, err := cc.ProcessQuery(userInput)
		if err != nil {
			fmt.Printf("请求失败: %v\n", err)
			continue
		}

		fmt.Printf("Assistant: %s\n", response)
	}
}

// 内层for循环用于单次Reasoning-Action循环
func (cc *ChatClient) ProcessQuery(userInput string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Second)
	defer cancel()

	// 列出所有可用工具
	availableTools := []openai.Tool{
		{
			Type: "function",
			Function: &openai.FunctionDefinition{
				Name:        "getWeather",
				Description: "Get weather for a given city",
				Parameters: json.RawMessage(`{
					"type": "object",
					"properties": {
						"city": { "type": "string" }
					},
					"required": ["city"]
				}`),
			},
		},
		{
			Type: "function",
			Function: &openai.FunctionDefinition{
				Name:        "getTime",
				Description: "Get current time for a given city",
				Parameters: json.RawMessage(`{
					"type": "object",
					"properties": {
						"city": { "type": "string" }
					},
					"required": ["city"]
				}`),
			},
		},
	}

	// 存储助理回复的消息
	var finalText string

	// 把用户输入加到上下文，开始对话
	cc.messages = append(cc.messages, openai.ChatCompletionMessage{
		Role:    openai.ChatMessageRoleUser,
		Content: userInput,
	})

	for {
		// 调用模型推理
		resp, err := cc.openaiClient.CreateChatCompletion(ctx, openai.ChatCompletionRequest{
			Model:    cc.model,
			Messages: cc.messages,
			Tools:    availableTools,
		})
		if err != nil {
			return "", err
		}

		choice := resp.Choices[0]
		message := choice.Message

		// 如果模型直接给出回答，结束循环
		if message.Content != "" {
			finalText = message.Content
			cc.messages = append(cc.messages, message)
			break

		} else if len(message.ToolCalls) > 0 { // 若调用工具
			toolCallMessages := []openai.ChatCompletionMessage{}

			for _, toolCall := range message.ToolCalls {
				fnName := toolCall.Function.Name
				fn, ok := cc.funcs[fnName]
				if !ok {
					log.Printf("函数未注册: %s", fnName)
					continue
				}
				var args map[string]any
				if err := json.Unmarshal([]byte(toolCall.Function.Arguments), &args); err != nil {
					log.Printf("参数解析失败: %v", err)
					continue
				}
				result := fn(args)

				toolCallMessages = append(toolCallMessages, openai.ChatCompletionMessage{
					Role:       openai.ChatMessageRoleTool,
					ToolCallID: toolCall.ID,
					Content:    result,
				})
			}

			// 先把助理调用工具的声明消息加入上下文
			cc.messages = append(cc.messages, openai.ChatCompletionMessage{
				Role:      openai.ChatMessageRoleAssistant,
				Content:   "",
				ToolCalls: message.ToolCalls,
			})

			// 把工具结果作为 observation 添加到上下文
			cc.messages = append(cc.messages, toolCallMessages...)
		}
	}

	// 拼接最终回答
	return finalText, nil
}

func getWeather(args map[string]any) string {
	city, _ := args["city"].(string)
	weatherData := map[string]string{
		"New York":      "Sunny, 25°C",
		"Tokyo":         "Cloudy, 22°C",
		"San Francisco": "Foggy, 18°C",
	}
	if val, ok := weatherData[city]; ok {
		return val
	}
	return "未知城市的天气"
}

func getTime(args map[string]any) string {
	city, _ := args["city"].(string)
	timeData := map[string]string{
		"New York":      "14:30 PM",
		"Tokyo":         "03:30 AM",
		"San Francisco": "11:30 AM",
	}
	if val, ok := timeData[city]; ok {
		return val
	}
	return "未知城市的时间"
}
