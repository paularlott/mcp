package mcp

import (
	"context"
	"fmt"
)

// ToolHandler represents a function that handles tool calls
type ToolHandler func(ctx context.Context, req *ToolRequest) (*ToolResponse, error)

// ToolRequest provides typed access to tool arguments
type ToolRequest struct {
	args map[string]interface{}
}

func (r *ToolRequest) String(name string) (string, error) {
	val, ok := r.args[name]
	if !ok {
		return "", fmt.Errorf("parameter '%s' not found", name)
	}
	if str, ok := val.(string); ok {
		return str, nil
	}
	return "", fmt.Errorf("parameter '%s' is not a string", name)
}

func (r *ToolRequest) StringOr(name, defaultValue string) string {
	if val, err := r.String(name); err == nil {
		return val
	}
	return defaultValue
}

func (r *ToolRequest) Int(name string) (int, error) {
	val, ok := r.args[name]
	if !ok {
		return 0, fmt.Errorf("parameter '%s' not found", name)
	}
	switch v := val.(type) {
	case int:
		return v, nil
	case float64:
		return int(v), nil
	default:
		return 0, fmt.Errorf("parameter '%s' is not a number", name)
	}
}

func (r *ToolRequest) IntOr(name string, defaultValue int) int {
	if val, err := r.Int(name); err == nil {
		return val
	}
	return defaultValue
}

func (r *ToolRequest) Float(name string) (float64, error) {
	val, ok := r.args[name]
	if !ok {
		return 0, fmt.Errorf("parameter '%s' not found", name)
	}
	if num, ok := val.(float64); ok {
		return num, nil
	}
	return 0, fmt.Errorf("parameter '%s' is not a number", name)
}

func (r *ToolRequest) FloatOr(name string, defaultValue float64) float64 {
	if val, err := r.Float(name); err == nil {
		return val
	}
	return defaultValue
}

func (r *ToolRequest) Bool(name string) (bool, error) {
	val, ok := r.args[name]
	if !ok {
		return false, fmt.Errorf("parameter '%s' not found", name)
	}
	if b, ok := val.(bool); ok {
		return b, nil
	}
	return false, fmt.Errorf("parameter '%s' is not a boolean", name)
}

func (r *ToolRequest) BoolOr(name string, defaultValue bool) bool {
	if val, err := r.Bool(name); err == nil {
		return val
	}
	return defaultValue
}
