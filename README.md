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
