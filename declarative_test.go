package mcp

import (
	"encoding/json"
	"testing"
)

func TestDeclarativeAPI(t *testing.T) {
	// Test tool with all parameter types and structured output
	tool := NewTool("comprehensive_test", "Test all parameter types",
		// Basic types
		String("name", "User name", Required()),
		Number("age", "User age"),
		Boolean("active", "Is user active"),
		
		// Arrays
		StringArray("tags", "User tags"),
		NumberArray("scores", "Test scores", Required()),
		
		// Objects
		Object("address", "User address",
			String("street", "Street address", Required()),
			String("city", "City name", Required()),
			Number("zipcode", "ZIP code"),
		),
		
		Object("profile", "User profile",
			String("bio", "User biography"),
			Boolean("public", "Is profile public"),
			Required(),
		),
		
		// Object arrays
		ObjectArray("contacts", "Contact list",
			String("name", "Contact name", Required()),
			String("email", "Contact email"),
		),
		
		ObjectArray("orders", "Order history",
			String("id", "Order ID", Required()),
			Number("total", "Order total", Required()),
			Required(),
		),
		
		// Structured output
		Output(
			String("user_id", "Created user ID"),
			NumberArray("processed_scores", "Processed scores"),
			Object("result", "Processing result",
				String("status", "Processing status"),
				Number("confidence", "Confidence score"),
			),
			ObjectArray("notifications", "Generated notifications",
				String("type", "Notification type"),
				String("message", "Notification message"),
			),
		),
	)

	// Test input schema
	inputSchema := tool.BuildSchema()
	inputJSON, err := json.MarshalIndent(inputSchema, "", "  ")
	if err != nil {
		t.Fatalf("Failed to marshal input schema: %v", err)
	}
	
	t.Logf("Input Schema:\n%s", string(inputJSON))
	
	// Verify input schema structure
	props := inputSchema["properties"].(map[string]interface{})
	
	// Check required fields
	required := inputSchema["required"].([]string)
	expectedRequired := []string{"name", "scores", "profile", "orders"}
	if len(required) != len(expectedRequired) {
		t.Errorf("Expected %d required fields, got %d", len(expectedRequired), len(required))
		t.Errorf("Expected: %v, Got: %v", expectedRequired, required)
	}
	
	// Check object array parameter
	contactsParam := props["contacts"].(map[string]interface{})
	if contactsParam["type"] != "array" {
		t.Errorf("Expected contacts type to be array, got %v", contactsParam["type"])
	}
	contactsItems := contactsParam["items"].(map[string]interface{})
	if contactsItems["type"] != "object" {
		t.Errorf("Expected contacts items type to be object, got %v", contactsItems["type"])
	}
	
	// Check required object array parameter
	ordersParam := props["orders"].(map[string]interface{})
	if ordersParam["type"] != "array" {
		t.Errorf("Expected orders type to be array, got %v", ordersParam["type"])
	}
	
	// Check string parameter
	nameParam := props["name"].(map[string]interface{})
	if nameParam["type"] != "string" {
		t.Errorf("Expected name type to be string, got %v", nameParam["type"])
	}
	
	// Check array parameter
	scoresParam := props["scores"].(map[string]interface{})
	if scoresParam["type"] != "array" {
		t.Errorf("Expected scores type to be array, got %v", scoresParam["type"])
	}
	scoresItems := scoresParam["items"].(map[string]interface{})
	if scoresItems["type"] != "number" {
		t.Errorf("Expected scores items type to be number, got %v", scoresItems["type"])
	}
	
	// Check object parameter
	addressParam := props["address"].(map[string]interface{})
	if addressParam["type"] != "object" {
		t.Errorf("Expected address type to be object, got %v", addressParam["type"])
	}
	addressProps := addressParam["properties"].(map[string]interface{})
	streetParam := addressProps["street"].(map[string]interface{})
	if streetParam["type"] != "string" {
		t.Errorf("Expected street type to be string, got %v", streetParam["type"])
	}

	// Test output schema
	outputSchema := tool.BuildOutputSchema()
	outputJSON, err := json.MarshalIndent(outputSchema, "", "  ")
	if err != nil {
		t.Fatalf("Failed to marshal output schema: %v", err)
	}
	
	t.Logf("Output Schema:\n%s", string(outputJSON))
	
	// Verify output schema structure
	outputProps := outputSchema["properties"].(map[string]interface{})
	
	// Check output string parameter
	userIdParam := outputProps["user_id"].(map[string]interface{})
	if userIdParam["type"] != "string" {
		t.Errorf("Expected user_id type to be string, got %v", userIdParam["type"])
	}
	
	// Check output array parameter
	processedScoresParam := outputProps["processed_scores"].(map[string]interface{})
	if processedScoresParam["type"] != "array" {
		t.Errorf("Expected processed_scores type to be array, got %v", processedScoresParam["type"])
	}
	
	// Check output object parameter
	resultParam := outputProps["result"].(map[string]interface{})
	if resultParam["type"] != "object" {
		t.Errorf("Expected result type to be object, got %v", resultParam["type"])
	}
	
	// Check output object array parameter
	notificationsParam := outputProps["notifications"].(map[string]interface{})
	if notificationsParam["type"] != "array" {
		t.Errorf("Expected notifications type to be array, got %v", notificationsParam["type"])
	}
	notificationsItems := notificationsParam["items"].(map[string]interface{})
	if notificationsItems["type"] != "object" {
		t.Errorf("Expected notifications items type to be object, got %v", notificationsItems["type"])
	}
}

func TestDeclarativeAPIWithoutOutput(t *testing.T) {
	// Test tool without structured output
	tool := NewTool("simple_test", "Simple test without output",
		String("message", "Input message", Required()),
		Number("count", "Repeat count"),
	)

	// Test input schema
	inputSchema := tool.BuildSchema()
	props := inputSchema["properties"].(map[string]interface{})
	
	if len(props) != 2 {
		t.Errorf("Expected 2 input parameters, got %d", len(props))
	}
	
	// Test output schema (should be nil)
	outputSchema := tool.BuildOutputSchema()
	if outputSchema != nil {
		t.Errorf("Expected nil output schema, got %v", outputSchema)
	}
}

func TestRequiredOption(t *testing.T) {
	tool := NewTool("required_test", "Test required option",
		String("optional", "Optional parameter"),
		String("required", "Required parameter", Required()),
	)

	inputSchema := tool.BuildSchema()
	required := inputSchema["required"].([]string)
	
	if len(required) != 1 {
		t.Errorf("Expected 1 required field, got %d", len(required))
	}
	
	if required[0] != "required" {
		t.Errorf("Expected required field to be 'required', got %s", required[0])
	}
}