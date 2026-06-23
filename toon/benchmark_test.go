package toon

import (
	"testing"
)

func BenchmarkEncode(b *testing.B) {
	// Create a moderately complex data structure
	data := map[string]any{
		"users": []any{
			map[string]any{
				"id":    1,
				"name":  "Alice",
				"email": "alice@example.com",
				"tags":  []any{"admin", "active"},
			},
			map[string]any{
				"id":    2,
				"name":  "Bob",
				"email": "bob@example.com",
				"tags":  []any{"user", "inactive"},
			},
		},
		"config": map[string]any{
			"debug":   true,
			"timeout": 30,
			"servers": []any{"server1", "server2", "server3"},
		},
		"metrics": []any{
			map[string]any{"cpu": 45.2, "memory": 78.1},
			map[string]any{"cpu": 52.8, "memory": 82.3},
			map[string]any{"cpu": 38.9, "memory": 71.5},
		},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := Encode(data)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkDecode(b *testing.B) {
	toonData := `config:
  debug: true
  servers[3]: server1,server2,server3
  timeout: 30
metrics[3]{cpu,memory}:
  45.2,78.1
  52.8,82.3
  38.9,71.5
users[2]:
  - id: 1
    name: Alice
    email: alice@example.com
    tags[2]: admin,active
  - id: 2
    name: Bob
    email: bob@example.com
    tags[2]: user,inactive`

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := Decode(toonData)
		if err != nil {
			b.Fatal(err)
		}
	}
}
