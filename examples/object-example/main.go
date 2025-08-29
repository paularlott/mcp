package main

import (
	"context"
	"fmt"
	"log"
	"net/http"

	"github.com/paularlott/mcp"
)

func main() {
	// Create server
	server := mcp.NewServer("object-example-server", "1.0.0")

	// Register a tool that accepts an object parameter
	server.RegisterTool(
		mcp.NewTool("create_user", "Create a new user with profile information",
			mcp.Object("user", "User information",
				mcp.String("name", "User's full name", mcp.Required()),
				mcp.String("email", "User's email address", mcp.Required()),
				mcp.Number("age", "User's age"),
				mcp.Boolean("active", "Whether user is active"),
				mcp.Required(),
			),
			mcp.Boolean("notify", "Send notification email"),
		),
		handleCreateUser,
	)

	// Register a tool that accepts an array of objects
	server.RegisterTool(
		mcp.NewTool("process_orders", "Process multiple orders",
			mcp.ObjectArray("orders", "List of orders to process",
				mcp.String("id", "Order ID", mcp.Required()),
				mcp.Number("amount", "Order amount", mcp.Required()),
				mcp.String("currency", "Currency code"),
				mcp.Object("customer", "Customer information",
					mcp.String("name", "Customer name", mcp.Required()),
					mcp.String("email", "Customer email"),
					mcp.Required(),
				),
				mcp.Required(),
			),
		),
		handleProcessOrders,
	)

	// Register a tool with nested objects
	server.RegisterTool(
		mcp.NewTool("create_product", "Create a product with detailed specifications",
			mcp.Object("product", "Product information",
				mcp.String("name", "Product name", mcp.Required()),
				mcp.Number("price", "Product price", mcp.Required()),
				mcp.Object("specifications", "Product specifications"),
				mcp.Required(),
			),
		),
		handleCreateProduct,
	)

	// Start server
	http.HandleFunc("/mcp", server.HandleRequest)
	fmt.Println("Object example server starting on :8001")
	fmt.Println("Try these example requests:")
	fmt.Println()
	fmt.Println("1. Create user:")
	fmt.Println(`curl -X POST http://localhost:8001/mcp \
  -H "Content-Type: application/json" \
  -d '{
    "jsonrpc": "2.0",
    "id": 1,
    "method": "tools/call",
    "params": {
      "name": "create_user",
      "arguments": {
        "user": {
          "name": "John Doe",
          "email": "john@example.com",
          "age": 30,
          "active": true
        },
        "notify": true
      }
    }
  }'`)
	fmt.Println()
	fmt.Println("2. Process orders:")
	fmt.Println(`curl -X POST http://localhost:8001/mcp \
  -H "Content-Type: application/json" \
  -d '{
    "jsonrpc": "2.0",
    "id": 2,
    "method": "tools/call",
    "params": {
      "name": "process_orders",
      "arguments": {
        "orders": [
          {
            "id": "ORD-001",
            "amount": 99.99,
            "currency": "USD",
            "customer": {
              "name": "Alice Smith",
              "email": "alice@example.com"
            }
          },
          {
            "id": "ORD-002", 
            "amount": 149.50,
            "currency": "EUR",
            "customer": {
              "name": "Bob Johnson",
              "email": "bob@example.com"
            }
          }
        ]
      }
    }
  }'`)

	log.Fatal(http.ListenAndServe(":8001", nil))
}

func handleCreateUser(ctx context.Context, req *mcp.ToolRequest) (*mcp.ToolResponse, error) {
	// Extract the user object
	user, err := req.Object("user")
	if err != nil {
		return nil, err
	}

	// Extract properties from the user object
	name, err := req.GetObjectStringProperty("user", "name")
	if err != nil {
		return nil, err
	}

	email, err := req.GetObjectStringProperty("user", "email")
	if err != nil {
		return nil, err
	}

	// Optional properties with defaults
	age := 0
	if ageVal, exists := user["age"]; exists {
		if ageFloat, ok := ageVal.(float64); ok {
			age = int(ageFloat)
		}
	}

	active := true
	if activeVal, exists := user["active"]; exists {
		if activeBool, ok := activeVal.(bool); ok {
			active = activeBool
		}
	}

	notify := req.BoolOr("notify", false)

	result := fmt.Sprintf("Created user: %s (%s), Age: %d, Active: %t, Notify: %t",
		name, email, age, active, notify)

	return mcp.NewToolResponseText(result), nil
}

func handleProcessOrders(ctx context.Context, req *mcp.ToolRequest) (*mcp.ToolResponse, error) {
	orders, err := req.ObjectSlice("orders")
	if err != nil {
		return nil, err
	}

	var results []string
	totalAmount := 0.0

	for i, order := range orders {
		id, ok := order["id"].(string)
		if !ok {
			return nil, fmt.Errorf("order %d missing or invalid id", i)
		}

		amount, ok := order["amount"].(float64)
		if !ok {
			return nil, fmt.Errorf("order %d missing or invalid amount", i)
		}

		currency := "USD"
		if curr, exists := order["currency"]; exists {
			if currStr, ok := curr.(string); ok {
				currency = currStr
			}
		}

		customer, ok := order["customer"].(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("order %d missing or invalid customer", i)
		}

		customerName, ok := customer["name"].(string)
		if !ok {
			return nil, fmt.Errorf("order %d customer missing name", i)
		}

		totalAmount += amount
		results = append(results, fmt.Sprintf("Processed order %s: %.2f %s for %s",
			id, amount, currency, customerName))
	}

	summary := fmt.Sprintf("Processed %d orders. Total amount: %.2f", len(orders), totalAmount)
	results = append(results, summary)

	return mcp.NewToolResponseText(fmt.Sprintf("%s\n\nDetails:\n- %s",
		summary, fmt.Sprintf("%s", results))), nil
}

func handleCreateProduct(ctx context.Context, req *mcp.ToolRequest) (*mcp.ToolResponse, error) {
	product, err := req.Object("product")
	if err != nil {
		return nil, err
	}

	name, ok := product["name"].(string)
	if !ok {
		return nil, fmt.Errorf("product name is required and must be a string")
	}

	price, ok := product["price"].(float64)
	if !ok {
		return nil, fmt.Errorf("product price is required and must be a number")
	}

	result := fmt.Sprintf("Created product: %s, Price: $%.2f", name, price)

	// Handle optional nested specifications object
	if specs, exists := product["specifications"]; exists {
		if specsObj, ok := specs.(map[string]interface{}); ok {
			result += "\nSpecifications:"
			for key, value := range specsObj {
				result += fmt.Sprintf("\n- %s: %v", key, value)
			}
		}
	}

	return mcp.NewToolResponseText(result), nil
}
