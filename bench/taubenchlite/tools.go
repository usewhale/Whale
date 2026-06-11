package main

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/usewhale/whale/internal/core"
)

type retailTool struct {
	name        string
	description string
	parameters  map[string]any
	readOnly    bool
	fn          func(map[string]any) (any, bool)
}

func (t retailTool) Name() string {
	return t.name
}

func (t retailTool) Description() string {
	return t.description
}

func (t retailTool) Parameters() map[string]any {
	return t.parameters
}

func (t retailTool) ReadOnly() bool {
	return t.readOnly
}

func (t retailTool) Run(_ context.Context, call core.ToolCall) (core.ToolResult, error) {
	args := map[string]any{}
	if err := json.Unmarshal([]byte(call.Input), &args); err != nil {
		return core.ToolResult{
			ToolCallID: call.ID,
			Name:       t.name,
			ModelText:  fmt.Sprintf(`{"error":"invalid json args: %s"}`, err.Error()),
			Outcome:    core.OutcomeFailure,
		}, nil
	}
	out, isErr := t.fn(args)
	b, err := json.Marshal(out)
	if err != nil {
		return core.ToolResult{
			ToolCallID: call.ID,
			Name:       t.name,
			ModelText:  fmt.Sprintf(`{"error":"marshal tool result: %s"}`, err.Error()),
			Outcome:    core.OutcomeFailure,
		}, nil
	}
	return core.ToolResult{
		ToolCallID: call.ID,
		Name:       t.name,
		ModelText:  string(b),
		Outcome:    benchOutcome(isErr),
	}, nil
}

func buildRetailTools(db *worldState) []core.Tool {
	return []core.Tool{
		lookupOrderTool(db),
		lookupUserTool(db),
		updateAddressTool(db),
		cancelOrderTool(db),
		refundOrderTool(db),
		listUserOrdersTool(db),
	}
}

func lookupOrderTool(db *worldState) core.Tool {
	return retailTool{
		name:        "lookup_order",
		description: "Look up an order by id. Returns { userId, status, address, item, price } or an error.",
		readOnly:    true,
		parameters: objectSchema(map[string]any{
			"orderId": stringSchema(),
		}, []string{"orderId"}),
		fn: func(args map[string]any) (any, bool) {
			orderID := stringArg(args, "orderId")
			row, ok := db.Orders[orderID]
			if !ok {
				return map[string]any{"error": "order not found"}, true
			}
			return map[string]any{
				"orderId": orderID,
				"userId":  row.UserID,
				"status":  row.Status,
				"address": row.Address,
				"item":    row.Item,
				"price":   row.Price,
			}, false
		},
	}
}

func lookupUserTool(db *worldState) core.Tool {
	return retailTool{
		name:        "lookup_user",
		description: "Look up a user by id. Returns { name, email } or an error.",
		readOnly:    true,
		parameters: objectSchema(map[string]any{
			"userId": stringSchema(),
		}, []string{"userId"}),
		fn: func(args map[string]any) (any, bool) {
			userID := stringArg(args, "userId")
			row, ok := db.Users[userID]
			if !ok {
				return map[string]any{"error": "user not found"}, true
			}
			return map[string]any{
				"userId": userID,
				"name":   row.Name,
				"email":  row.Email,
			}, false
		},
	}
}

func updateAddressTool(db *worldState) core.Tool {
	return retailTool{
		name:        "update_address",
		description: "Update the shipping address on an order. Only allowed if status is 'processing'. Returns ok or error.",
		parameters: objectSchema(map[string]any{
			"orderId": stringSchema(),
			"address": stringSchema(),
		}, []string{"orderId", "address"}),
		fn: func(args map[string]any) (any, bool) {
			orderID := stringArg(args, "orderId")
			address := stringArg(args, "address")
			row, ok := db.Orders[orderID]
			if !ok {
				return map[string]any{"error": "order not found"}, true
			}
			if row.Status != "processing" {
				return map[string]any{"error": "cannot edit: status=" + row.Status}, true
			}
			row.Address = address
			db.Orders[orderID] = row
			return map[string]any{"ok": true, "orderId": orderID, "newAddress": address}, false
		},
	}
}

func cancelOrderTool(db *worldState) core.Tool {
	return retailTool{
		name:        "cancel_order",
		description: "Cancel an order. Only allowed if status is 'processing'. Returns ok or error.",
		parameters: objectSchema(map[string]any{
			"orderId": stringSchema(),
		}, []string{"orderId"}),
		fn: func(args map[string]any) (any, bool) {
			orderID := stringArg(args, "orderId")
			row, ok := db.Orders[orderID]
			if !ok {
				return map[string]any{"error": "order not found"}, true
			}
			if row.Status != "processing" {
				return map[string]any{"error": "cannot cancel: status=" + row.Status}, true
			}
			row.Status = "cancelled"
			db.Orders[orderID] = row
			return map[string]any{"ok": true, "orderId": orderID, "status": "cancelled"}, false
		},
	}
}

func refundOrderTool(db *worldState) core.Tool {
	return retailTool{
		name:        "refund_order",
		description: "Issue a refund on a delivered order. Records an entry in refunds. Returns ok or error.",
		parameters: objectSchema(map[string]any{
			"orderId": stringSchema(),
			"reason":  stringSchema(),
		}, []string{"orderId", "reason"}),
		fn: func(args map[string]any) (any, bool) {
			orderID := stringArg(args, "orderId")
			reason := stringArg(args, "reason")
			row, ok := db.Orders[orderID]
			if !ok {
				return map[string]any{"error": "order not found"}, true
			}
			if row.Status != "delivered" {
				return map[string]any{"error": "cannot refund: status=" + row.Status}, true
			}
			db.Refunds[orderID] = refundRow{OrderID: orderID, Reason: reason, Amount: row.Price}
			row.Status = "refunded"
			db.Orders[orderID] = row
			return map[string]any{"ok": true, "orderId": orderID, "amount": row.Price}, false
		},
	}
}

func listUserOrdersTool(db *worldState) core.Tool {
	return retailTool{
		name:        "list_user_orders",
		description: "List every order belonging to a userId. Returns an array of orders.",
		readOnly:    true,
		parameters: objectSchema(map[string]any{
			"userId": stringSchema(),
		}, []string{"userId"}),
		fn: func(args map[string]any) (any, bool) {
			userID := stringArg(args, "userId")
			orderIDs := make([]string, 0, len(db.Orders))
			for orderID, row := range db.Orders {
				if row.UserID == userID {
					orderIDs = append(orderIDs, orderID)
				}
			}
			sort.Strings(orderIDs)
			out := make([]map[string]any, 0, len(orderIDs))
			for _, orderID := range orderIDs {
				row := db.Orders[orderID]
				out = append(out, map[string]any{
					"orderId": orderID,
					"userId":  row.UserID,
					"status":  row.Status,
					"address": row.Address,
					"item":    row.Item,
					"price":   row.Price,
				})
			}
			return out, false
		},
	}
}

func objectSchema(properties map[string]any, required []string) map[string]any {
	return map[string]any{
		"type":                 "object",
		"properties":           properties,
		"required":             required,
		"additionalProperties": false,
	}
}

func stringSchema() map[string]any {
	return map[string]any{"type": "string"}
}

func stringArg(args map[string]any, name string) string {
	if v, ok := args[name].(string); ok {
		return v
	}
	return ""
}

func stubArgs(t core.Tool) string {
	spec := core.DescribeTool(t)
	props, _ := spec.Parameters["properties"].(map[string]any)
	out := map[string]string{}
	for name := range props {
		out[name] = "stub"
	}
	b, _ := json.Marshal(out)
	return string(b)
}

func benchOutcome(isErr bool) core.ToolOutcome {
	if isErr {
		return core.OutcomeFailure
	}
	return core.OutcomeSuccess
}
