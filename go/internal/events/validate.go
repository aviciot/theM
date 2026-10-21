package events

import "fmt"

// ValidateEvent checks if an event has required fields for its kind
func ValidateEvent(e Event) error {
	if e.Kind == "" {
		return fmt.Errorf("missing event kind")
	}
	if e.EventID == "" {
		return fmt.Errorf("missing event_id")
	}
	if e.TenantID == "" {
		return fmt.Errorf("missing tenant_id")
	}

	switch e.Kind {
	case EventKindGatewayRequest:
		if e.GatewayRequest == nil {
			return fmt.Errorf("gateway_request event has nil GatewayRequest payload")
		}
		if e.GatewayRequest.RequestID == "" {
			return fmt.Errorf("gateway_request missing request_id")
		}

	case EventKindToolCall:
		if e.ToolCall == nil {
			return fmt.Errorf("tool_call event has nil ToolCall payload")
		}

	case EventKindUserActivity:
		if e.UserActivity == nil {
			return fmt.Errorf("user_activity event has nil UserActivity payload")
		}
	}

	return nil
}
