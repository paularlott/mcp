package openai

import (
	"encoding/json"
	"strings"
)

// toolLoopStream merges the native response streams of a tool-calling loop,
// in which the client runs MCP tools itself, into one Responses lifecycle,
// as emulation produces: a single response.created and response.in_progress,
// output items renumbered across rounds with the internal function_call
// items hidden, sequence numbers renumbered, and only the last round's
// response.completed, carrying every forwarded output item and the combined
// usage. That completed response keeps the last round's ID, which is the one
// to continue from with previous_response_id.
type toolLoopStream struct {
	round    int
	seq      int
	next     int          // next merged output_index
	indexes  map[int]int  // this round: upstream output_index -> merged
	hidden   map[int]bool // this round: upstream output_index of hidden items
	output   []any        // forwarded output items, in merged order
	usage    ResponseUsage
	sawUsage bool // whether any round reported usage
}

// startRound prepares for the next upstream stream.
func (s *toolLoopStream) startRound() {
	s.round++
	s.indexes = map[int]int{}
	s.hidden = map[int]bool{}
}

// rewrite returns the event to forward in place of ev, or false to drop it.
func (s *toolLoopStream) rewrite(ev ResponseStreamEvent) (ResponseStreamEvent, bool) {
	var data map[string]any
	if err := json.Unmarshal(ev.Data, &data); err != nil {
		return ev, true
	}

	switch ev.Type {
	case "response.created", "response.in_progress", "response.queued":
		if s.round > 1 {
			return ev, false
		}
	case "response.completed":
		resp := ev.Response()
		if resp == nil {
			return ev, true
		}
		if resp.Usage != nil {
			s.usage.Add(resp.Usage)
			s.sawUsage = true
		}
		if hasResponseToolCalls(resp) {
			return ev, false // the loop runs the tools and continues
		}
		if r, ok := data["response"].(map[string]any); ok {
			r["output"] = append([]any{}, s.output...)
			if s.sawUsage {
				r["usage"] = toJSONValue(s.usage)
			}
		}
	}
	if strings.HasPrefix(ev.Type, "response.function_call") || strings.HasPrefix(ev.Type, "response.tool_call") {
		return ev, false
	}
	if idx, ok := intField(data, "output_index"); ok {
		if ev.Type == "response.output_item.added" {
			if item, _ := data["item"].(map[string]any); isFunctionCallItem(item) {
				s.hidden[idx] = true
			}
		}
		if s.hidden[idx] {
			return ev, false
		}
		merged, ok := s.indexes[idx]
		if !ok {
			merged = s.next
			s.next++
			s.indexes[idx] = merged
		}
		data["output_index"] = merged
		if ev.Type == "response.output_item.done" {
			if item, ok := data["item"]; ok {
				s.output = append(s.output, item)
			}
		}
	}
	if _, ok := data["sequence_number"]; ok {
		data["sequence_number"] = s.seq
		s.seq++
	}

	raw, err := json.Marshal(data)
	if err != nil {
		return ev, true
	}
	return ResponseStreamEvent{Type: ev.Type, Data: raw}, true
}

func isFunctionCallItem(item map[string]any) bool {
	t, _ := item["type"].(string)
	return t == "function_call" || t == "tool_call"
}

func intField(data map[string]any, key string) (int, bool) {
	f, ok := data[key].(float64)
	return int(f), ok
}

func toJSONValue(v any) any {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	var out any
	_ = json.Unmarshal(raw, &out)
	return out
}
