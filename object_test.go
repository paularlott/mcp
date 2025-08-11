package mcp

import (
	"encoding/json"
	"testing"
)

func TestObjectSchemaGeneration(t *testing.T) {
	// Test simple object parameter
	tool := NewTool("test_tool", "Test tool with object parameter").
		AddObjectParam("user", "User information", true).
		AddProperty("name", String, "User's name", true).
		AddProperty("age", Number, "User's age", false).
		Done()

	schema := tool.buildSchema()

	// Verify the schema structure
	properties, ok := schema["properties"].(map[string]interface{})
	if !ok {
		t.Fatal("Expected properties to be a map")
	}

	userProp, ok := properties["user"].(map[string]interface{})
	if !ok {
		t.Fatal("Expected user property to be a map")
	}

	if userProp["type"] != "object" {
		t.Errorf("Expected user type to be 'object', got %v", userProp["type"])
	}

	userProperties, ok := userProp["properties"].(map[string]interface{})
	if !ok {
		t.Fatal("Expected user properties to be a map")
	}

	// Check name property
	nameProp, ok := userProperties["name"].(map[string]interface{})
	if !ok {
		t.Fatal("Expected name property to be a map")
	}
	if nameProp["type"] != "string" {
		t.Errorf("Expected name type to be 'string', got %v", nameProp["type"])
	}

	// Check age property
	ageProp, ok := userProperties["age"].(map[string]interface{})
	if !ok {
		t.Fatal("Expected age property to be a map")
	}
	if ageProp["type"] != "number" {
		t.Errorf("Expected age type to be 'number', got %v", ageProp["type"])
	}

	// Check required fields
	userRequired, ok := userProp["required"].([]string)
	if !ok {
		t.Fatal("Expected user required to be a string slice")
	}
	if len(userRequired) != 1 || userRequired[0] != "name" {
		t.Errorf("Expected required to be ['name'], got %v", userRequired)
	}
}

func TestArrayObjectSchemaGeneration(t *testing.T) {
	// Test array of objects parameter
	tool := NewTool("test_tool", "Test tool with array of objects").
		AddArrayObjectParam("items", "List of items", true).
		AddProperty("id", String, "Item ID", true).
		AddProperty("quantity", Number, "Item quantity", true).
		Done()

	schema := tool.buildSchema()

	properties, ok := schema["properties"].(map[string]interface{})
	if !ok {
		t.Fatal("Expected properties to be a map")
	}

	itemsProp, ok := properties["items"].(map[string]interface{})
	if !ok {
		t.Fatal("Expected items property to be a map")
	}

	if itemsProp["type"] != "array" {
		t.Errorf("Expected items type to be 'array', got %v", itemsProp["type"])
	}

	itemsSchema, ok := itemsProp["items"].(map[string]interface{})
	if !ok {
		t.Fatal("Expected items schema to be a map")
	}

	if itemsSchema["type"] != "object" {
		t.Errorf("Expected items schema type to be 'object', got %v", itemsSchema["type"])
	}

	itemProperties, ok := itemsSchema["properties"].(map[string]interface{})
	if !ok {
		t.Fatal("Expected item properties to be a map")
	}

	// Check id property
	idProp, ok := itemProperties["id"].(map[string]interface{})
	if !ok {
		t.Fatal("Expected id property to be a map")
	}
	if idProp["type"] != "string" {
		t.Errorf("Expected id type to be 'string', got %v", idProp["type"])
	}
}

func TestGenericObjectSchema(t *testing.T) {
	// Test generic object (no properties defined)
	tool := NewTool("test_tool", "Test tool with generic object").
		AddParam("config", Object, "Configuration object", true)

	schema := tool.buildSchema()

	properties, ok := schema["properties"].(map[string]interface{})
	if !ok {
		t.Fatal("Expected properties to be a map")
	}

	configProp, ok := properties["config"].(map[string]interface{})
	if !ok {
		t.Fatal("Expected config property to be a map")
	}

	if configProp["type"] != "object" {
		t.Errorf("Expected config type to be 'object', got %v", configProp["type"])
	}

	// Generic objects should allow additional properties
	if configProp["additionalProperties"] != true {
		t.Errorf("Expected generic object to allow additional properties")
	}
}

func TestComplexNestedObjectSchema(t *testing.T) {
	// Test complex nested object structure
	tool := NewTool("test_tool", "Test tool with nested objects").
		AddObjectParam("order", "Order information", true).
		AddProperty("id", String, "Order ID", true).
		AddProperty("total", Number, "Order total", true).
		AddObjectProperty("customer", "Customer information", true).
		Done()

	schema := tool.buildSchema()

	// Convert to JSON and back to verify it's valid JSON Schema
	jsonBytes, err := json.Marshal(schema)
	if err != nil {
		t.Fatalf("Failed to marshal schema to JSON: %v", err)
	}

	var unmarshaled map[string]interface{}
	err = json.Unmarshal(jsonBytes, &unmarshaled)
	if err != nil {
		t.Fatalf("Failed to unmarshal schema from JSON: %v", err)
	}

	// Print the schema for manual inspection
	t.Logf("Generated schema: %s", string(jsonBytes))
}
