package main

import (
	"fmt"

	"github.com/paularlott/mcp"
)

func main() {
	// Configure the default namespace separator globally
	// This affects all NewClient calls throughout the application
	mcp.DefaultNamespaceSeparator = "|"

	// Create clients - they will all use the configured separator
	client1 := mcp.NewClient("https://api1.example.com/mcp", nil, "service1")
	client2 := mcp.NewClient("https://api2.example.com/mcp", nil, "service2")

	fmt.Printf("Client 1 namespace: %s\n", client1.Namespace()) // Output: service1|
	fmt.Printf("Client 2 namespace: %s\n", client2.Namespace()) // Output: service2|

	// You can also change it at runtime for new clients
	mcp.DefaultNamespaceSeparator = "::"
	client3 := mcp.NewClient("https://api3.example.com/mcp", nil, "service3")
	fmt.Printf("Client 3 namespace: %s\n", client3.Namespace()) // service3::

	// Existing clients are unaffected
	fmt.Printf("Client 1 namespace (unchanged): %s\n", client1.Namespace()) // service1|
}
