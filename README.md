# AI Agent

基于Funcation Call的新版本Reasoning Action流程

## 新版本:

新版本通过把 `tool` 的调用结果作为 `tool 角色`的消息，连同之前的所有对话上下文一起发送给大模型

## 旧版本(略):

通过 `system prompt` 定义

```
You are an AI agent. You can use tools to answer questions.
Use the following format:

Thought: <your reasoning>
Action: <tool name>
Input: <input to the tool>

... (tool output returned here) ...

Then continue reasoning, or say:

Final Answer: <your answer>
```

通过 `parseActionResponse` 函数获取: `Thought`(推理), `Action`(工具), `Input`(工具的参数), `Final Answer`(最终回答)

还需要把 `Action + Input` 的函数调用结果作为新的`system prompt`连同之前的所有消息一起发送给大模型

```
func parseActionResponse(content string) (thought, action, input, finalAnswer string) {
	var current string

	lines := strings.Split(content, "\n")

	for _, line := range lines {
		switch {
		case strings.HasPrefix(line, "Final Answer:"):
			finalAnswer = strings.TrimPrefix(line, "Final Answer:")
			current = "final"
		case strings.HasPrefix(line, "Thought:"):
			thought = strings.TrimPrefix(line, "Thought:")
			current = "thought"
		case strings.HasPrefix(line, "Action:"):
			action = strings.TrimPrefix(line, "Action:")
			current = "action"
		case strings.HasPrefix(line, "Input:"):
			input = strings.TrimPrefix(line, "Input:")
			current = "input"

		default:
			switch current {
			case "thought":
				thought += line
			case "action":
				action += line
			case "input":
				input += line
			case "final":
				finalAnswer += line
			}
		}
	}

	fmt.Println("Thought:", thought)
	fmt.Println("Action:", action)
	fmt.Println("Input:", input)
	fmt.Println("Final Answer:", finalAnswer)

	return
}
```

## 运行

```
go run tools/ip_location_query/main.go &
go run main.go

Initializing client...
[ip-location-query] Connected to server: ip-location-server 1.0.0
Initializing client...
[calculator] Connected to server: calculator-server 1.0.0
Type your queries or 'quit' to exit.
User: new york weather and time and 1+2=? where's my ip location
2025/07/02 22:46:13 function call
2025/07/02 22:46:13 function call
2025/07/02 22:46:13 mcp call
2025/07/02 22:46:13 mcp call
2025/07/02 22:46:13 工具调用失败: 无效的 IP 地址
Assistant: Here's the information you requested:

- **Weather in New York**: Sunny, 25°C.
- **Current Time in New York**: 14:30 PM.
- **1 + 2 = 3**.

For your IP location, could you provide your IP address so I can check its location?

User: 183.193.157.229
2025/07/02 22:47:01 mcp call
Assistant: Here's the location information for the IP address **183.193.157.229**:

- **Country**: China (CN)
- **Region**: Shanghai (SH)
- **City**: Shanghai
- **Coordinates**: Latitude 31.2222, Longitude 121.4581
- **Timezone**: Asia/Shanghai
- **ISP**: China Mobile Communications Corporation
- **Organization**: China Mobile Communications Corporation - Shanghai Company

Let me know if you'd like any additional details!
```
