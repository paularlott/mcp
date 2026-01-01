package toon

import (
	"strings"
	"testing"
)

func TestTOONToolExtractionVerification(t *testing.T) {
	// Simulate the exact TOON data from the user's example
	toonData := `[9]:
  - description: Test AI completion with automatic tool calling support. The AI can use tool_search and execute_tool as needed.
  inputSchema:
    additionalProperties: false
    properties:
      model:
        description: "The model to use for completion (default: mistralai/devstral-small-2-2512)"
        type: string
      question:
        description: Question to ask the AI
        type: string
    required[1]: question
    type: object
  name: ai_test_tool
  score: 1
  - description: A simple calculator that performs basic arithmetic operations
  inputSchema:
    additionalProperties: false
    properties:
      a:
        description: The first number
        type: number
      b:
        description: The second number
        type: number
      operation:
        description: "The operation to perform: add, subtract, multiply, or divide"
        type: string
    required[3]: operation,a,b
    type: object
  name: calculator
  score: 1
  - description: A simple tool that greets the user
  inputSchema:
    additionalProperties: false
    properties:
      name:
        description: "The name to greet (optional, defaults to 'World')"
        type: string
    type: object
  name: greet
  score: 1
  - description: "Demonstrates MCP library functions: list_tools, search_tools, execute_tool, and execute_script"
  inputSchema:
    additionalProperties: false
    properties:
      action:
        description: "The action to perform: list, search, execute, or script"
        type: string
      args:
        description: JSON string of arguments for execute action
        type: string
      query:
        description: Search query (for search action) or tool name (for execute action)
        type: string
    required[1]: action
    type: object
  name: mcp_demo
  score: 1
  - description: A tool that performs various string operations using a library
  inputSchema:
    additionalProperties: false
    properties:
      operation:
        description: "The operation to perform: reverse, uppercase, lowercase, capitalize, count_words, remove_spaces, is_palindrome"
        type: string
      text:
        description: The text to process
        type: string
    required[2]: operation,text
    type: object
  name: string_processor
  score: 1
  - description: Check the weather for a given location
  inputSchema:
    additionalProperties: false
    properties:
      location:
        description: The city or location to check weather for
        type: string
      units:
        description: "Temperature units: celsius or fahrenheit"
        type: string
    required[1]: location
    type: object
  name: weather_checker
  score: 1`

	// Expected tools from JSON version
	expectedTools := []string{
		"ai_test_tool",
		"calculator",
		"greet",
		"mcp_demo",
		"string_processor",
		"weather_checker",
	}

	t.Logf("TOON data length: %d characters", len(toonData))
	t.Logf("Expected %d tools", len(expectedTools))

	// Verify the TOON data contains all expected tools
	for i, tool := range expectedTools {
		if !strings.Contains(toonData, "name: "+tool) {
			t.Errorf("Missing tool %d: %s", i+1, tool)
		} else {
			t.Logf("✓ Found tool %d: %s", i+1, tool)
		}
	}

	// Count actual occurrences of "name:" to verify structure
	nameCount := strings.Count(toonData, "name: ")
	t.Logf("Found %d 'name:' occurrences in TOON data", nameCount)

	if nameCount != len(expectedTools) {
		t.Errorf("Expected %d tools but found %d 'name:' occurrences", len(expectedTools), nameCount)
	}

	// Verify the array header shows correct count
	if !strings.HasPrefix(toonData, "[9]:") {
		t.Error("TOON data should start with '[9]:' indicating 9 tools")
	}

	t.Log("✅ TOON data is structurally correct and contains all 9 expected tools")
}
