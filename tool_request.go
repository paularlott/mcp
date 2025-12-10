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

// NewToolRequest creates a new ToolRequest with the given arguments
func NewToolRequest(args map[string]interface{}) *ToolRequest {
	return &ToolRequest{args: args}
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

// Object returns a parameter as a map[string]interface{} (generic object)
func (r *ToolRequest) Object(name string) (map[string]interface{}, error) {
	val, ok := r.args[name]
	if !ok {
		return nil, ErrUnknownParameter
	}
	if obj, ok := val.(map[string]interface{}); ok {
		return obj, nil
	}
	return nil, fmt.Errorf("parameter '%s' is not an object", name)
}

// ObjectOr returns a parameter as an object or the default value
func (r *ToolRequest) ObjectOr(name string, defaultValue map[string]interface{}) map[string]interface{} {
	if val, err := r.Object(name); err == nil {
		return val
	}
	return defaultValue
}

// ObjectSlice returns a parameter as a slice of objects
func (r *ToolRequest) ObjectSlice(name string) ([]map[string]interface{}, error) {
	val, ok := r.args[name]
	if !ok {
		return nil, ErrUnknownParameter
	}
	if arr, ok := val.([]interface{}); ok {
		result := make([]map[string]interface{}, len(arr))
		for i, item := range arr {
			if obj, ok := item.(map[string]interface{}); ok {
				result[i] = obj
			} else {
				return nil, fmt.Errorf("parameter '%s' contains non-object element at index %d", name, i)
			}
		}
		return result, nil
	}
	return nil, fmt.Errorf("parameter '%s' is not an array", name)
}

// ObjectSliceOr returns a parameter as a slice of objects or the default value
func (r *ToolRequest) ObjectSliceOr(name string, defaultValue []map[string]interface{}) []map[string]interface{} {
	if val, err := r.ObjectSlice(name); err == nil {
		return val
	}
	return defaultValue
}

// GetObjectProperty extracts a property from an object parameter
func (r *ToolRequest) GetObjectProperty(objectName, propertyName string) (interface{}, error) {
	obj, err := r.Object(objectName)
	if err != nil {
		return nil, err
	}
	val, ok := obj[propertyName]
	if !ok {
		return nil, fmt.Errorf("property '%s' not found in object '%s'", propertyName, objectName)
	}
	return val, nil
}

// GetObjectStringProperty extracts a string property from an object parameter
func (r *ToolRequest) GetObjectStringProperty(objectName, propertyName string) (string, error) {
	val, err := r.GetObjectProperty(objectName, propertyName)
	if err != nil {
		return "", err
	}
	if str, ok := val.(string); ok {
		return str, nil
	}
	return "", fmt.Errorf("property '%s' in object '%s' is not a string", propertyName, objectName)
}

// GetObjectIntProperty extracts an int property from an object parameter
func (r *ToolRequest) GetObjectIntProperty(objectName, propertyName string) (int, error) {
	val, err := r.GetObjectProperty(objectName, propertyName)
	if err != nil {
		return 0, err
	}
	switch v := val.(type) {
	case int:
		return v, nil
	case float64:
		return int(v), nil
	default:
		return 0, fmt.Errorf("property '%s' in object '%s' is not a number", propertyName, objectName)
	}
}

// GetObjectBoolProperty extracts a bool property from an object parameter
func (r *ToolRequest) GetObjectBoolProperty(objectName, propertyName string) (bool, error) {
	val, err := r.GetObjectProperty(objectName, propertyName)
	if err != nil {
		return false, err
	}
	if b, ok := val.(bool); ok {
		return b, nil
	}
	return false, fmt.Errorf("property '%s' in object '%s' is not a boolean", propertyName, objectName)
}
