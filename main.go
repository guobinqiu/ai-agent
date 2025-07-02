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

	"github.com/go-playground/validator"
	"github.com/joho/godotenv"
	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/sashabaranov/go-openai"
)

type fn func(map[string]any) string

type ChatClient struct {
	openaiClient *openai.Client
	model        string
	messages     []openai.ChatCompletionMessage // 用于存储历史消息，实现多轮对话吗，先不做裁剪
	funcs        map[string]fn
	mcpClients   []*client.Client
}

type MCPConfig struct {
	MCPServers map[string]MCPServer `json:"mcpServers"`
}

type MCPServer struct {
	Type    string   `json:"type" validate:"required"`
	Command string   `json:"command" validate:"required"`
	Args    []string `json:"args,omitempty"`
}

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	mcpClients, errs := LoadMCPClients("config.json", ctx)
	if len(errs) > 0 {
		for _, err := range errs {
			log.Println(err)
		}
	}
	defer func() {
		for _, mcpClient := range mcpClients {
			mcpClient.Close()
		}
	}()

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
		mcpClients:   mcpClients,
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

	// 维护toolName到mcpClient的映射
	toolNameMap := make(map[string]*client.Client)

	for _, mcpClient := range cc.mcpClients {
		toolsResp, err := mcpClient.ListTools(ctx, mcp.ListToolsRequest{})
		if err != nil {
			log.Printf("Failed to list tools: %v", err)
		}
		for _, tool := range toolsResp.Tools {
			// fmt.Println("name:", tool.Name)
			// fmt.Println("description:", tool.Description)
			// fmt.Println("parameters:", tool.InputSchema)
			availableTools = append(availableTools, openai.Tool{
				Type: openai.ToolTypeFunction,
				Function: &openai.FunctionDefinition{
					Name:        tool.Name,
					Description: tool.Description,
					Parameters:  tool.InputSchema,
				},
			})

			toolNameMap[tool.Name] = mcpClient
		}
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
			successfulToolCalls := []openai.ToolCall{}

			for _, toolCall := range message.ToolCalls {
				var result string

				toolName := toolCall.Function.Name
				_, ok := toolNameMap[toolName]
				if !ok { // function call
					log.Println("function call")
					fnName := toolName
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
					result = fn(args)
				} else { // mcp call
					log.Println("mcp call")
					toolArgsRaw := toolCall.Function.Arguments
					// fmt.Println("=====toolCall.Function.Arguments:", toolArgsRaw)
					var toolArgs map[string]any
					_ = json.Unmarshal([]byte(toolArgsRaw), &toolArgs)

					// 调用工具
					req := mcp.CallToolRequest{
						Params: mcp.CallToolParams{
							Name:      toolName,
							Arguments: toolArgs,
						},
					}
					mcpClient := toolNameMap[toolName]
					resp, err := mcpClient.CallTool(ctx, req)
					if err != nil {
						log.Printf("工具调用失败: %v", err)
						continue
					}
					result = fmt.Sprintf("%s", resp.Content)
				}

				toolCallMessages = append(toolCallMessages, openai.ChatCompletionMessage{
					Role:       openai.ChatMessageRoleTool,
					ToolCallID: toolCall.ID,
					Content:    result,
				})
				successfulToolCalls = append(successfulToolCalls, toolCall)
			}

			// 先把助理调用工具的声明消息加入上下文
			cc.messages = append(cc.messages, openai.ChatCompletionMessage{
				Role:      openai.ChatMessageRoleAssistant,
				Content:   "",
				ToolCalls: successfulToolCalls,
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

// 创建客户端实例，连接 MCP 服务端
func LoadMCPClients(configPath string, ctx context.Context) ([]*client.Client, []error) {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, []error{err}
	}

	var mcpConfig MCPConfig
	err = json.Unmarshal(data, &mcpConfig)
	if err != nil {
		return nil, []error{err}
	}

	if err := validator.New().Struct(mcpConfig); err != nil {
		return nil, []error{err}
	}

	var mcpClients []*client.Client
	var errors []error

	for name, mcpServer := range mcpConfig.MCPServers {
		var mcpClient *client.Client
		var err error

		switch strings.ToLower(mcpServer.Type) {
		case "stdio":
			mcpClient, err = client.NewStdioMCPClient(mcpServer.Command, mcpServer.Args)
		case "http":
			mcpClient, err = client.NewStreamableHttpClient(mcpServer.Command)
		case "sse":
			mcpClient, err = client.NewSSEMCPClient(mcpServer.Command)
		default:
			err = fmt.Errorf("未知服务类型: %s (%s)", name, mcpServer.Type)
		}

		if err != nil {
			errors = append(errors, fmt.Errorf("[%s] 创建客户端失败: %v", name, err))
			continue
		}

		// 初始化 MCP 客户端
		fmt.Println("Initializing client...")
		initRequest := mcp.InitializeRequest{}
		initRequest.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
		initRequest.Params.ClientInfo = mcp.Implementation{
			Name:    name, // 使用配置中的名称作为客户端名
			Version: "1.0.0",
		}
		initResult, err := mcpClient.Initialize(ctx, initRequest)
		if err != nil {
			errors = append(errors, fmt.Errorf("[%s] 初始化失败: %v", name, err))
			continue
		}

		fmt.Printf("[%s] Connected to server: %s %s\n", name, initResult.ServerInfo.Name, initResult.ServerInfo.Version)

		mcpClients = append(mcpClients, mcpClient)
	}

	return mcpClients, errors
}
