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
		return "", ErrUnknownParameter
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
		return 0, ErrUnknownParameter
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
		return 0, ErrUnknownParameter
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
		return false, ErrUnknownParameter
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

func (r *ToolRequest) StringSlice(name string) ([]string, error) {
	val, ok := r.args[name]
	if !ok {
		return nil, ErrUnknownParameter
	}
	if arr, ok := val.([]interface{}); ok {
		result := make([]string, len(arr))
		for i, item := range arr {
			if str, ok := item.(string); ok {
				result[i] = str
			} else {
				return nil, fmt.Errorf("parameter '%s' contains non-string element at index %d", name, i)
			}
		}
		return result, nil
	}
	return nil, fmt.Errorf("parameter '%s' is not an array", name)
}

func (r *ToolRequest) StringSliceOr(name string, defaultValue []string) []string {
	if val, err := r.StringSlice(name); err == nil {
		return val
	}
	return defaultValue
}

func (r *ToolRequest) IntSlice(name string) ([]int, error) {
	val, ok := r.args[name]
	if !ok {
		return nil, ErrUnknownParameter
	}
	if arr, ok := val.([]interface{}); ok {
		result := make([]int, len(arr))
		for i, item := range arr {
			switch v := item.(type) {
			case int:
				result[i] = v
			case float64:
				result[i] = int(v)
			default:
				return nil, fmt.Errorf("parameter '%s' contains non-number element at index %d", name, i)
			}
		}
		return result, nil
	}
	return nil, fmt.Errorf("parameter '%s' is not an array", name)
}

func (r *ToolRequest) IntSliceOr(name string, defaultValue []int) []int {
	if val, err := r.IntSlice(name); err == nil {
		return val
	}
	return defaultValue
}

func (r *ToolRequest) FloatSlice(name string) ([]float64, error) {
	val, ok := r.args[name]
	if !ok {
		return nil, ErrUnknownParameter
	}
	if arr, ok := val.([]interface{}); ok {
		result := make([]float64, len(arr))
		for i, item := range arr {
			if num, ok := item.(float64); ok {
				result[i] = num
			} else {
				return nil, fmt.Errorf("parameter '%s' contains non-number element at index %d", name, i)
			}
		}
		return result, nil
	}
	return nil, fmt.Errorf("parameter '%s' is not an array", name)
}

func (r *ToolRequest) FloatSliceOr(name string, defaultValue []float64) []float64 {
	if val, err := r.FloatSlice(name); err == nil {
		return val
	}
	return defaultValue
}
